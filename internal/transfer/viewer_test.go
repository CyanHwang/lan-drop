package transfer

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rowTag(body, rel string) string {
	for _, part := range strings.Split(body, "<li ") {
		if !strings.Contains(part, `data-rel="`+rel+`"`) {
			continue
		}
		if i := strings.IndexByte(part, '>'); i >= 0 {
			return part[:i]
		}
	}
	return ""
}

// Actions are behind one trigger per row. The old page shipped two exposed
// buttons per row, which made a listing of a hundred photos look like a
// control panel rather than a file browser.
func TestPageUsesRowMenu(t *testing.T) {
	_, base, root, _ := newTestServer(t)
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, body := getPage(t, base+"/s/test/", nil)
	if strings.Contains(body, `class="act-btn dl"`) || strings.Contains(body, `data-del=`) {
		t.Fatal("row still renders its own exposed action buttons")
	}
	if n := strings.Count(body, "data-more="); n != 3 {
		t.Fatalf("%d three-dot triggers for 3 rows", n)
	}
	for _, id := range []string{"rowMenu", "viewer", "viewerStage", "viewerImg", "viewerVideo", "viewerDoc", "viewerFrame"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Fatalf("page is missing #%s", id)
		}
	}
	if strings.Contains(body, `id="menuOpen"`) {
		t.Fatal("folder menu still carries an open action")
	}
}

// The server decides which rows get a "view" item. The browser never has to
// guess from an extension whether a preview component exists.
func TestPageMarksViewableFiles(t *testing.T) {
	_, base, root, _ := newTestServer(t)
	for _, n := range []string{"photo.jpg", "notes.md", "sheet.xlsx", "clip.mp4", "plain.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, body := getPage(t, base+"/s/test/", nil)
	for _, n := range []string{"photo.jpg", "notes.md", "sheet.xlsx", "clip.mp4"} {
		tag := rowTag(body, n)
		if !strings.Contains(tag, `data-view="1"`) {
			t.Fatalf("%s is not marked viewable: %s", n, tag)
		}
	}
	if tag := rowTag(body, "plain.txt"); !strings.Contains(tag, `data-view="0"`) {
		t.Fatalf("plain.txt should not be viewable: %s", tag)
	}
	// The file name itself is the second way into the viewer.
	if !strings.Contains(body, `data-view-open="note.md"`) && !strings.Contains(body, `data-view-open="notes.md"`) {
		t.Fatal("viewable file name is not clickable")
	}
	if strings.Contains(body, `data-view-open="plain.txt"`) {
		t.Fatal("plain.txt name should not open the viewer")
	}
	if tag := rowTag(body, "photo.jpg"); !strings.Contains(tag, "data-created=") || !strings.Contains(tag, "data-modified=") {
		t.Fatalf("viewer metadata missing from row: %s", tag)
	}
}

// Formats the browser renders natively must be served as the original file.
// Re-encoding a JPEG to a fixed 1600 px preview dropped its EXIF orientation —
// a phone's portrait photo opened sideways — and flattened animated GIFs.
func TestNativeImagesUseTheOriginalFile(t *testing.T) {
	_, base, root, _ := newTestServer(t)
	for _, n := range []string{"photo.jpg", "anim.gif", "scan.tiff"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, body := getPage(t, base+"/s/test/", nil)
	if tag := rowTag(body, "photo.jpg"); !strings.Contains(tag, `data-viewer=""`) {
		t.Fatalf("JPEG should not be re-encoded for the viewer: %s", tag)
	}
	if tag := rowTag(body, "anim.gif"); !strings.Contains(tag, `data-viewer=""`) {
		t.Fatalf("GIF should not be re-encoded for the viewer: %s", tag)
	}
	// No browser decodes TIFF, so that one does go through the renderer.
	if tag := rowTag(body, "scan.tiff"); !strings.Contains(tag, "size=large") {
		t.Fatalf("TIFF should get a rendered preview: %s", tag)
	}
}

// Video is viewable, and the page carries whether the host can transcode a
// format the browser refuses — that flag is the difference between "download
// it" and a silent broken player.
func TestPageMarksVideoPlayable(t *testing.T) {
	_, base, root, _ := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, body := getPage(t, base+"/s/test/", nil)
	if tag := rowTag(body, "clip.mp4"); !strings.Contains(tag, `data-view="1"`) {
		t.Fatalf("video is not offered to the viewer: %s", tag)
	}
	if !strings.Contains(body, `data-ffmpeg="`) {
		t.Fatal("page does not report whether the host can transcode")
	}
	if !strings.Contains(body, `<video id="viewerVideo"`) {
		t.Fatal("viewer has no video element")
	}
}

// Preview requests ask for the bytes inline, not as an attachment, with a
// MIME type the browser can actually use. A <video> fed octet-stream with
// nosniff refuses to play a file it could decode perfectly.
func TestInlineVideoContentType(t *testing.T) {
	_, base, root, _ := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("not really"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := do(t, "GET", base+"/api/download?share=test&path=clip.mp4&inline=1", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q, want video/mp4", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}

// Preview requests ask for the bytes inline, not as an attachment, with a
// MIME type the browser can actually use.
func TestInlineDownload(t *testing.T) {
	_, base, root, _ := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := do(t, "GET", base+"/api/download?share=test&path=notes.md&inline=1", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline;") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("Content-Type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "# Hello\n" {
		t.Fatalf("body = %q", body)
	}

	// The normal download still forces a save.
	plain := do(t, "GET", base+"/api/download?share=test&path=notes.md", nil, nil)
	plain.Body.Close()
	if cd := plain.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("plain download = %q", cd)
	}
}

// Viewer libraries are embedded and immutable, like the stylesheet and script.
func TestVendorAssetsAreServed(t *testing.T) {
	_, base, _, _ := newTestServer(t)
	for _, name := range []string{"marked.min.js", "purify.min.js", "docx-preview.min.js", "jszip.min.js", "xlsx.mini.min.js"} {
		resp := do(t, "GET", base+"/static/vendor/"+name, nil, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body) < 1000 {
			t.Fatalf("%s -> %d, %d bytes", name, resp.StatusCode, len(body))
		}
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Fatalf("%s Cache-Control = %q", name, cc)
		}
	}
}
