package transfer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// previewMeta is the handful of facts the viewer can show about a file. Every
// field is optional: a format that has no DPI, or a machine without ffprobe,
// simply reports less, and the page hides what it did not get.
type previewMeta struct {
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	DPI       int     `json:"dpi,omitempty"`
	FrameRate float64 `json:"frameRate,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
	Codec     string  `json:"codec,omitempty"`
}

// FfprobePath returns the companion binary of ffmpeg. It is optional for the
// same reason ffmpeg is: no bundled native code, no extra install for the
// common case.
func FfprobePath() string {
	p, err := exec.LookPath("ffprobe")
	if err != nil {
		return ""
	}
	return p
}

// handlePreviewInfo answers one viewer's worth of metadata. This is a lazy
// endpoint on purpose: probing every row of a listing would turn a directory
// of videos into a minute of ffprobe launches.
func (s *Server) handlePreviewInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rel := strings.Trim(q.Get("path"), "/")
	if rel == "" {
		fail(w, http.StatusBadRequest, "missing path")
		return
	}
	abs, _, err := s.resolve(s.langFor(r), q.Get("share"), rel)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}

	var meta previewMeta
	switch {
	case nativeImage(abs):
		meta = imageMeta(abs)
	case isVideoName(abs):
		meta = s.probeMeta(abs, true)
	case stillFrameFormat(abs):
		// HEIC and AVIF are single pictures in a container ffprobe also
		// understands. It reports them as a one-frame video at 90000 fps, which
		// is technically true and completely useless in a photo's footer.
		meta = s.probeMeta(abs, false)
	default:
		// Markdown, PDF, docx, xlsx: there is nothing here the viewer does not
		// already show. Returning early also keeps a folder of documents from
		// spawning ffprobe per opened file.
	}
	writeJSON(w, http.StatusOK, meta)
}

// stillFrameFormat is the set of image formats the browser cannot decode but
// ffmpeg can.
func stillFrameFormat(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".heic", ".heif", ".avif":
		return true
	}
	return false
}

func imageMeta(abs string) previewMeta {
	var meta previewMeta
	f, err := os.Open(abs)
	if err != nil {
		return meta
	}
	defer f.Close()
	if cfg, _, err := image.DecodeConfig(f); err == nil {
		meta.Width, meta.Height = cfg.Width, cfg.Height
	}
	// DPI lives in a header near the start of the file; reading the whole of a
	// 12 MP photo just for two numbers would be wasteful.
	data := make([]byte, 64<<10)
	n, _ := f.ReadAt(data, 0)
	if n > 0 {
		ext := strings.ToLower(filepath.Ext(abs))
		switch ext {
		case ".jpg", ".jpeg":
			meta.DPI = jpegDPI(data[:n])
		case ".png":
			meta.DPI = pngDPI(data[:n])
		}
	}
	return meta
}

// jpegDPI walks the marker segments until the first scan, reading the JFIF
// density. EXIF-only photos simply report no DPI rather than guessing.
func jpegDPI(b []byte) int {
	if len(b) < 4 || b[0] != 0xff || b[1] != 0xd8 {
		return 0
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xff {
			i++
			continue
		}
		m := b[i+1]
		if m == 0xd8 || m == 0x01 || (m >= 0xd0 && m <= 0xd7) {
			i += 2
			continue
		}
		if m == 0xd9 || m == 0xda {
			break
		}
		n := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		if n < 2 || i+2+n > len(b) {
			break
		}
		seg := b[i+2 : i+2+n]
		// seg starts with its two-byte length; the JFIF payload follows.
		if m == 0xe0 && len(seg) >= 16 && string(seg[2:7]) == "JFIF\x00" {
			units := seg[9]
			x := int(binary.BigEndian.Uint16(seg[10:12]))
			switch units {
			case 1: // dots per inch
				return x
			case 2: // dots per centimetre
				return int(math.Round(float64(x) * 2.54))
			}
		}
		i += 2 + n
	}
	return 0
}

// pngDPI reads the pHYs chunk's pixels-per-metre value.
func pngDPI(b []byte) int {
	if len(b) < 16 || string(b[:8]) != "\x89PNG\r\n\x1a\n" {
		return 0
	}
	for i := 8; i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i : i+4]))
		typ := string(b[i+4 : i+8])
		if i+12+n > len(b) {
			break
		}
		if typ == "pHYs" && n >= 9 && b[i+16] == 1 {
			x := binary.BigEndian.Uint32(b[i+8 : i+12])
			return int(math.Round(float64(x) * 0.0254))
		}
		i += 12 + n
	}
	return 0
}

// probeMeta uses ffprobe for video and for formats Go cannot decode (HEIC,
// AVIF). moving is false for those still pictures, which drops the timing
// fields ffprobe invents for them. A zero struct comes back on any failure; the
// viewer has its own placeholder for "not available".
func (s *Server) probeMeta(abs string, moving bool) previewMeta {
	bin := FfprobePath()
	if bin == "" {
		return previewMeta{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate,avg_frame_rate,codec_name",
		"-show_entries", "format=duration",
		"-of", "json", abs,
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = quietProc()
	out, err := cmd.Output()
	if err != nil {
		return previewMeta{}
	}

	var doc struct {
		Streams []struct {
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			RFrameRate   string `json:"r_frame_rate"`
			AvgFrameRate string `json:"avg_frame_rate"`
			CodecName    string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || len(doc.Streams) == 0 {
		return previewMeta{}
	}
	st := doc.Streams[0]
	meta := previewMeta{
		Width:  st.Width,
		Height: st.Height,
		Codec:  st.CodecName,
	}
	if !moving {
		return meta
	}
	// avg_frame_rate is the honest one for variable frame rate files.
	meta.FrameRate = parseRate(st.AvgFrameRate)
	if meta.FrameRate <= 0 {
		meta.FrameRate = parseRate(st.RFrameRate)
	}
	if d, err := strconv.ParseFloat(doc.Format.Duration, 64); err == nil && d > 0 {
		meta.Duration = d
	}
	return meta
}

// parseRate turns ffprobe's "30000/1001" into 29.97.
func parseRate(v string) float64 {
	if v == "" || v == "0/0" {
		return 0
	}
	if i := strings.IndexByte(v, '/'); i > 0 {
		num, e1 := strconv.ParseFloat(v[:i], 64)
		den, e2 := strconv.ParseFloat(v[i+1:], 64)
		if e1 != nil || e2 != nil || den == 0 {
			return 0
		}
		return num / den
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}
