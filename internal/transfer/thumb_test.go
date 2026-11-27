package transfer

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateThumbCache redirects the cache before a server is built: New()
// captures the directory when it constructs the cache object.
func isolateThumbCache(t *testing.T) {
	t.Helper()
	previous := thumbCacheDir
	dir := t.TempDir()
	thumbCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { thumbCacheDir = previous })
}

func writeTestJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}

func countThumbFiles(t *testing.T) int {
	t.Helper()
	dir, err := thumbCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return len(entries)
}

func TestThumbnailsOffByDefault(t *testing.T) {
	isolateThumbCache(t)
	_, base, root, share := newTestServer(t)
	writeTestJPEG(t, filepath.Join(root, "photo.jpg"), 200, 120)

	_, body := getPage(t, base+"/s/"+share+"/", nil)
	if strings.Contains(body, `src="/api/thumb?`) {
		t.Fatal("listing shows automatic previews while the switch is off")
	}

	resp := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("thumb with previews off -> %d, want 404", resp.StatusCode)
	}
}

func TestThumbRendersSquareJPEG(t *testing.T) {
	isolateThumbCache(t)
	s, base, root, share := newTestServer(t)
	writeTestJPEG(t, filepath.Join(root, "photo.jpg"), 600, 400)
	s.SetThumbnails(true)

	_, body := getPage(t, base+"/s/"+share+"/", nil)
	// html/template escapes the ampersands in an attribute value.
	if !strings.Contains(body, "/api/thumb?share="+share+"&amp;path=photo.jpg&amp;v=") {
		t.Fatal("page does not link the preview")
	}

	resp := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thumb -> %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("cache control = %q", cc)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	if img.Bounds().Dx() != thumbSize || img.Bounds().Dy() != thumbSize {
		t.Fatalf("decoded %dx%d, want %d", img.Bounds().Dx(), img.Bounds().Dy(), thumbSize)
	}
	if len(data) > 32<<10 {
		t.Fatalf("thumbnail is %d bytes, too big for an icon", len(data))
	}

	// The second request is served from the cache, not decoded again.
	before := countThumbFiles(t)
	again := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg", nil, nil)
	again.Body.Close()
	if again.StatusCode != 200 || countThumbFiles(t) != before {
		t.Fatal("second request did not come from the cache")
	}
}

// Replacing a file has to produce a new preview. The cache key includes the
// size and mtime, so no explicit invalidation is needed.
func TestThumbInvalidatesOnRewrite(t *testing.T) {
	isolateThumbCache(t)
	s, base, root, share := newTestServer(t)
	path := filepath.Join(root, "photo.jpg")
	writeTestJPEG(t, path, 600, 400)
	s.SetThumbnails(true)

	first := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg", nil, nil)
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first thumb -> %d", first.StatusCode)
	}

	writeTestJPEG(t, path, 300, 500) // different dimensions, different size
	second := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg", nil, nil)
	second.Body.Close()
	if second.StatusCode != 200 {
		t.Fatalf("second thumb -> %d", second.StatusCode)
	}
	if n := countThumbFiles(t); n != 2 {
		t.Fatalf("cache has %d entries, want 2 (old one becomes an orphan)", n)
	}

	// A different name at the same location must not be served from memory.
	_, body := getPage(t, base+"/s/"+share+"/", nil)
	if !strings.Contains(body, "photo.jpg") {
		t.Fatal("listing lost the file")
	}
}

func TestPurgeThumbCache(t *testing.T) {
	isolateThumbCache(t)
	s, base, root, share := newTestServer(t)
	writeTestJPEG(t, filepath.Join(root, "photo.jpg"), 200, 120)
	s.SetThumbnails(true)

	resp := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg", nil, nil)
	resp.Body.Close()
	if countThumbFiles(t) != 1 {
		t.Fatal("expected one cached preview")
	}

	freed, err := PurgeThumbCache()
	if err != nil {
		t.Fatal(err)
	}
	if freed <= 0 || countThumbFiles(t) != 0 {
		t.Fatalf("purge freed %d bytes, %d files left", freed, countThumbFiles(t))
	}
	// Purging twice is the normal path when a user clicks the button after the
	// switch already deleted the directory.
	if _, err := PurgeThumbCache(); err != nil {
		t.Fatalf("second purge: %v", err)
	}
}

func TestThumbRefusesTraversal(t *testing.T) {
	isolateThumbCache(t)
	s, base, root, share := newTestServer(t)
	writeTestJPEG(t, filepath.Join(root, "photo.jpg"), 200, 120)
	if err := os.MkdirAll(filepath.Join(root, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJPEG(t, filepath.Join(root, "private", "secret.jpg"), 200, 120)
	s.SetThumbnails(true)

	for _, p := range []string{"../private/secret.jpg", "%2e%2e%2f%2e%2e%2fetc/passwd"} {
		resp := do(t, "GET", base+"/api/thumb?share="+share+"&path="+p, nil, nil)
		resp.Body.Close()
		if resp.StatusCode == 200 {
			t.Fatalf("%s served a file outside the requested path", p)
		}
	}
}

func TestPreviewableFormats(t *testing.T) {
	for _, name := range []string{"a.jpg", "b.PNG", "c.gif", "d.webp", "e.bmp", "f.tiff"} {
		if !previewable(name, "") {
			t.Fatalf("%s should preview without ffmpeg", name)
		}
	}
	for _, name := range []string{"clip.mp4", "photo.heic", "photo.avif"} {
		if previewable(name, "") {
			t.Fatalf("%s should need ffmpeg", name)
		}
		if !previewable(name, "/usr/bin/ffmpeg") {
			t.Fatalf("%s should preview with ffmpeg", name)
		}
	}
	if previewable("notes.txt", "/usr/bin/ffmpeg") {
		t.Fatal("text files must not offer a preview")
	}
}

// A thumbnail generated from a future timestamp must still be picked up after
// the file is touched: the version in the URL comes from the same stat.
func TestThumbKeyChangesWithMtime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "photo.jpg")
	st := time.Unix(1_700_000_000, 0)
	first := thumbKey(path, 4096, st, "128")
	second := thumbKey(path, 4096, st.Add(time.Second), "128")
	if first == second {
		t.Fatal("mtime is not part of the cache key")
	}
}

// A full-screen preview is an explicit click, not an automatic thumbnail, so
// it must still work while the listing switch is off. The two sizes use
// different cache entries.
func TestLargePreviewWorksWithThumbnailsOff(t *testing.T) {
	isolateThumbCache(t)
	_, base, root, share := newTestServer(t)
	writeTestJPEG(t, filepath.Join(root, "photo.jpg"), 600, 400)

	small := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg", nil, nil)
	small.Body.Close()
	if small.StatusCode != 404 {
		t.Fatalf("automatic thumbnail off -> %d, want 404", small.StatusCode)
	}

	large := do(t, "GET", base+"/api/thumb?share="+share+"&path=photo.jpg&size=large", nil, nil)
	defer large.Body.Close()
	if large.StatusCode != 200 {
		t.Fatalf("explicit large preview -> %d", large.StatusCode)
	}
	data, err := io.ReadAll(large.Body)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	if img.Bounds().Dx() != viewerSize {
		t.Fatalf("preview is %d px, want %d", img.Bounds().Dx(), viewerSize)
	}
}
