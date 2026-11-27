package transfer

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

	// Decoders register themselves. JPEG and PNG come from the standard
	// library; the rest are pure-Go and cost nothing until they are used.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	_ "image/gif"
)

// Thumbnails are the one thing this server does that costs real CPU: a 12 MP
// photo takes ~50 ms to decode and ~46 MB of transient memory, so the original
// file is never handed to the browser. Everything below is about turning that
// cost into something paid once per file per version.
const (
	// thumbSize is the edge of the generated square. The icon slot is 38 CSS
	// px, which is 114 device px on a 3x phone; 128 leaves a little headroom.
	thumbSize = 128
	// viewerSize is the longest edge of a full-screen preview. The browser
	// cannot display HEIC/AVIF at all, and a 12 MP original is a poor fit for
	// a phone even when it can, so the viewer gets its own rendition.
	viewerSize = 1600
	// thumbQuality is deliberately mid-range: at this size the difference is
	// invisible and the file stays around 4 KB.
	thumbQuality = 80
	// thumbMaxSource refuses anything big enough to hurt. A 32 MB JPEG is
	// already a 100 MP image; beyond that this is a memory bomb, not a photo.
	thumbMaxSource = 32 << 20
	// thumbMaxPixels is the second guard, for small files that decode huge.
	thumbMaxPixels = 40_000_000
	// thumbWorkers bounds concurrent decodes. The same process streams
	// transfers at line rate; a folder of photos must not starve them.
	thumbWorkers = 2
	// thumbMemItems is the hot set: roughly two screens of icons.
	thumbMemItems = 512
	// thumbTTL is how long an unreferenced thumbnail survives. Deleting a
	// folder leaves its thumbnails behind, and nobody misses them after a week.
	thumbTTL = 7 * 24 * time.Hour
	// thumbDiskCap caps the whole cache. 4 KB each means ~64k images.
	thumbDiskCap = 256 << 20
	// thumbTimeout bounds one ffmpeg run. A damaged file must not hold a
	// worker forever.
	thumbTimeout = 20 * time.Second
)

// thumbPlate is what transparent images are composited onto. JPEG has no alpha,
// and a screenshot with a transparent background would otherwise come out
// black.
var thumbPlate = color.RGBA{R: 0xf2, G: 0xf3, B: 0xf6, A: 0xff}

var errNoThumb = errors.New("no thumbnail available")

// FfmpegPath reports the ffmpeg on PATH, or "" when there is none. It is the
// only optional dependency in the project: the binary stays portable and
// self-contained, and video (or HEIC) previews simply light up when a system
// ffmpeg happens to be installed.
func FfmpegPath() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

// ThumbCacheDir is the OS cache directory for generated previews. It is
// deliberately not inside a share: writing derived files into the user's
// folders would pollute them, show up in listings, and fail on read-only
// shares. A cache directory is also the one place a user is expected to be able
// to delete without losing anything.
func ThumbCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		// No home directory (a service account, a stripped container): temp is
		// still a cache location, and the sweeper keeps it bounded.
		return filepath.Join(os.TempDir(), "lan-drop-thumbs"), nil
	}
	return filepath.Join(base, "lan-drop", "thumbs"), nil
}

// thumbCacheDir is a variable so tests can point the cache at a temp directory
// instead of the real user's. Production has no reason to change it.
var thumbCacheDir = ThumbCacheDir

// ThumbCacheStats reports what the cache currently occupies.
func ThumbCacheStats() (files int, bytes int64) {
	dir, err := thumbCacheDir()
	if err != nil {
		return 0, 0
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes
}

// PurgeThumbCache deletes every generated preview and reports how much was
// freed. Safe to call at any time: the files are derived, never the only copy
// of anything.
func PurgeThumbCache() (int64, error) {
	dir, err := thumbCacheDir()
	if err != nil {
		return 0, err
	}
	_, bytes := ThumbCacheStats()
	if err := os.RemoveAll(dir); err != nil {
		return 0, err
	}
	return bytes, nil
}

// thumbKey identifies one version of one file. Including size and modification
// time is what makes invalidation disappear: a replaced file gets a new key, so
// there is no stale entry to hunt down and no "clear cache" step in the normal
// flow. The one case it cannot see is a file rewritten with identical size and
// an identical timestamp — which is what timestamp-preserving copies and
// coarse-grained (FAT) filesystems can produce. The failure mode is a stale
// 38 px icon until the file is touched again, which is why it is documented
// rather than solved with a content hash that would cost a read per row.
func thumbKey(abs string, size int64, mtime time.Time, variant string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(abs)))
	return hex.EncodeToString(sum[:8]) + "-" + strconv.FormatInt(size, 10) + "-" +
		strconv.FormatInt(mtime.UnixNano(), 10) + "-" + variant
}

// thumbCache is two tiers: a small LRU in memory for the rows on screen, and
// one file per thumbnail on disk for everything else. Both are keyed by
// thumbKey, so neither ever needs to know why a key changed.
type thumbCache struct {
	dir string
	sem chan struct{}

	mu    sync.Mutex
	ll    *list.List // front = most recently used
	items map[string]*list.Element
}

type thumbItem struct {
	key  string
	data []byte
}

func newThumbCache() *thumbCache {
	dir, _ := thumbCacheDir()
	return &thumbCache{
		dir:   dir,
		sem:   make(chan struct{}, thumbWorkers),
		ll:    list.New(),
		items: make(map[string]*list.Element),
	}
}

func (c *thumbCache) file(key string) string {
	return filepath.Join(c.dir, key+".jpg")
}

// load returns a cached thumbnail, warming the memory tier from disk.
func (c *thumbCache) load(key string) ([]byte, bool) {
	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		data := el.Value.(*thumbItem).data
		c.mu.Unlock()
		return data, true
	}
	c.mu.Unlock()

	data, err := os.ReadFile(c.file(key))
	if err != nil {
		return nil, false
	}
	c.remember(key, data)
	return data, true
}

// store publishes a freshly generated thumbnail to both tiers. The disk write
// is best effort: a read-only or full cache directory must not turn a working
// preview into an error.
func (c *thumbCache) store(key string, data []byte) {
	c.remember(key, data)
	if c.dir == "" {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	tmp := c.file(key) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, c.file(key))
}

func (c *thumbCache) remember(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&thumbItem{key: key, data: data})
	for c.ll.Len() > thumbMemItems {
		old := c.ll.Back()
		if old == nil {
			break
		}
		c.ll.Remove(old)
		delete(c.items, old.Value.(*thumbItem).key)
	}
}

// sweep drops thumbnails nobody has looked at for a week, then trims the
// oldest entries if the directory is still over its cap. Deleting a folder
// leaves its previews behind; this is what reclaims them.
func (c *thumbCache) sweep() {
	if c.dir == "" {
		return
	}
	type entry struct {
		path string
		size int64
		at   time.Time
	}
	var all []entry
	var total int64
	_ = filepath.WalkDir(c.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		all = append(all, entry{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})

	cutoff := time.Now().Add(-thumbTTL)
	kept := all[:0]
	for _, e := range all {
		if e.at.Before(cutoff) {
			if os.Remove(e.path) == nil {
				total -= e.size
			}
			continue
		}
		kept = append(kept, e)
	}

	if total > thumbDiskCap {
		// Oldest first: the newest entries are the ones the current session is
		// still showing.
		sort.Slice(kept, func(i, j int) bool { return kept[i].at.Before(kept[j].at) })
		for _, e := range kept {
			if total <= thumbDiskCap {
				break
			}
			if os.Remove(e.path) == nil {
				total -= e.size
			}
		}
	}
}

// previewable reports whether a name can be turned into a thumbnail at all.
// Formats the standard library (plus x/image) decodes are always available;
// video and HEIC need ffmpeg, so they only count when it is installed.
func previewable(name, ffmpeg string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".tif", ".tiff":
		return true
	case ".heic", ".heif", ".avif",
		".mp4", ".m4v", ".mov", ".mkv", ".webm", ".avi", ".wmv", ".flv", ".mpg", ".mpeg", ".3gp":
		return ffmpeg != ""
	}
	return false
}

func isVideoName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".m4v", ".mov", ".mkv", ".webm", ".avi", ".wmv", ".flv", ".mpg", ".mpeg", ".3gp":
		return true
	}
	return false
}

// handleThumb serves one square preview. The `v` parameter in the URL is for
// the browser's cache only; the key is always recomputed from the file on disk,
// so a page rendered a moment before the file changed can never poison the
// cache with the wrong bytes under the right name.
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	large := q.Get("size") == "large"
	// The listing switch controls automatic 128 px thumbnails. A full-screen
	// preview is an explicit click and still works when it is off.
	if !s.thumbsOn.Load() && !large {
		http.NotFound(w, r)
		return
	}
	rel := strings.Trim(q.Get("path"), "/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}

	abs, _, err := s.resolve(s.langFor(r), q.Get("share"), rel)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() || st.Size() > thumbMaxSource {
		http.NotFound(w, r)
		return
	}

	edge := thumbSize
	if large {
		edge = viewerSize
	}
	key := thumbKey(abs, st.Size(), st.ModTime(), strconv.Itoa(edge))
	if data, ok := s.thumbs.load(key); ok {
		writeThumb(w, data)
		return
	}

	// Bound the work: the browser opens six connections per host, and six
	// simultaneous 12 MP decodes is how a phone transfer stalls.
	select {
	case s.thumbs.sem <- struct{}{}:
		defer func() { <-s.thumbs.sem }()
	case <-r.Context().Done():
		return
	}

	data, err := s.renderThumb(abs, st, edge)
	if err != nil {
		// The page keeps its icon when this 404s, so an unsupported format is
		// simply a file without a preview.
		http.NotFound(w, r)
		return
	}
	s.thumbs.store(key, data)
	writeThumb(w, data)
}

func writeThumb(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	// The URL carries the file's size and mtime, so a changed file is a
	// different URL and this can be cached forever.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// renderThumb produces the JPEG bytes for one file.
func (s *Server) renderThumb(abs string, st os.FileInfo, edge int) ([]byte, error) {
	if isVideoName(abs) || !nativeImage(abs) {
		return s.ffmpegThumb(abs, edge)
	}
	img, err := decodeThumbSource(abs, st)
	if err != nil {
		return nil, err
	}
	return encodeThumb(coverThumb(img, edge))
}

func nativeImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".tif", ".tiff":
		return true
	}
	return false
}

func decodeThumbSource(abs string, st os.FileInfo) (image.Image, error) {
	if st.Size() > thumbMaxSource {
		return nil, errNoThumb
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Read the header before decoding: a 200 KB PNG can declare 50000x50000,
	// and decoding it would allocate 10 GB.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width*cfg.Height > thumbMaxPixels ||
		cfg.Width > 20000 || cfg.Height > 20000 {
		return nil, errNoThumb
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	return img, err
}

// coverThumb centre-crops to a square and scales it down. Large reductions go
// through halving first: a single bilinear step from 4032 px to 128 px samples
// four pixels and throws the rest away, which is visible as noise on any photo
// with fine detail.
func coverThumb(src image.Image, size int) image.Image {
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2

	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: thumbPlate}, image.Point{}, draw.Src)

	// SubImage is a view, not a copy: cropping a 12 MP JPEG must not allocate
	// another 46 MB before the first pixel is scaled.
	cur := image.Image(src)
	if si, ok := src.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		cur = si.SubImage(image.Rect(x0, y0, x0+side, y0+side))
	} else {
		// Every decoder this package registers returns a type with SubImage,
		// so this is a correctness net rather than a hot path.
		crop := image.NewRGBA(image.Rect(0, 0, side, side))
		draw.Draw(crop, crop.Bounds(), src, image.Pt(x0, y0), draw.Src)
		cur = crop
	}
	for cur.Bounds().Dx() > size*2 {
		cur = halve(cur)
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), cur, cur.Bounds(), draw.Over, nil)
	return dst
}

func halve(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx()/2, b.Dy()/2
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

func encodeThumb(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ffmpegThumb is the escape hatch for everything the standard library cannot
// read: video, HEIC, AVIF. ffmpeg is taken from PATH, never bundled, so the
// single-file promise holds and the feature is simply absent without it.
func (s *Server) ffmpegThumb(abs string, edge int) ([]byte, error) {
	bin := s.ffmpegPath()
	if bin == "" {
		return nil, errNoThumb
	}
	filter := fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d[v]",
		edge, edge, edge, edge)

	run := func(seek string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), thumbTimeout)
		defer cancel()
		args := []string{"-nostdin", "-loglevel", "error"}
		if seek != "" {
			// Before -i, so ffmpeg seeks the container instead of decoding
			// every frame up to that point.
			args = append(args, "-ss", seek)
		}
		args = append(args, "-i", abs, "-frames:v", "1",
			"-filter_complex", filter, "-map", "[v]",
			"-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "4", "pipe:1")
		cmd := exec.CommandContext(ctx, bin, args...)
		// A GUI host must not flash a console for each preview.
		cmd.SysProcAttr = quietProc()
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return nil, err
		}
		if out.Len() == 0 {
			return nil, errNoThumb
		}
		return out.Bytes(), nil
	}

	// Two seconds in is past the fade-in most videos open with. A clip shorter
	// than that has no frame there, so fall back to the first one.
	if isVideoName(abs) {
		if data, err := run("2"); err == nil {
			return data, nil
		}
	}
	return run("")
}

func (s *Server) ffmpegPath() string {
	if v, ok := s.ffmpeg.Load().(string); ok {
		return v
	}
	return ""
}

// SetThumbnails turns preview generation on or off while the server runs. The
// page is rendered per request, so the next listing simply stops carrying
// image URLs.
func (s *Server) SetThumbnails(on bool) {
	s.thumbsOn.Store(on)
	// PATH can change between launches; refreshing here also picks up an
	// ffmpeg the user installed after starting the app.
	s.ffmpeg.Store(FfmpegPath())
}
