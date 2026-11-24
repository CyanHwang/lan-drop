package transfer

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postZip drives the endpoint the way the browser UI does: a form-encoded POST,
// which is what keeps large selections out of the URL.
func postZip(t *testing.T, base string, vals url.Values) *http.Response {
	t.Helper()
	return do(t, "POST", base+"/api/download/zip",
		strings.NewReader(vals.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
}

func readZip(t *testing.T, base string, vals url.Values) *zip.Reader {
	t.Helper()
	resp := postZip(t, base, vals)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	return zr
}

func zipNames(zr *zip.Reader) []string {
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func readEntry(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	t.Fatalf("entry %q not found in %v", name, zipNames(zr))
	return ""
}

func TestZipSingleFileIsStored(t *testing.T) {
	_, base, root, share := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello lan"), 0o644); err != nil {
		t.Fatal(err)
	}

	zr := readZip(t, base, url.Values{"share": {share}, "paths": {"hello.txt"}})
	if len(zr.File) != 1 {
		t.Fatalf("entries = %v", zipNames(zr))
	}
	if got := zr.File[0].Name; got != "hello.txt" {
		t.Fatalf("name = %q", got)
	}
	// Store, not Deflate: the whole point is not burning a core on the LAN.
	if zr.File[0].Method != zip.Store {
		t.Fatalf("method = %d, want Store", zr.File[0].Method)
	}
	if got := readEntry(t, zr, "hello.txt"); got != "hello lan" {
		t.Fatalf("content = %q", got)
	}
}

func TestZipFolderKeepsLayout(t *testing.T) {
	_, base, root, share := newTestServer(t)
	dir := filepath.Join(root, "photos", "2024")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photos", "b.jpg"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}

	zr := readZip(t, base, url.Values{"share": {share}, "paths": {"photos"}})
	names := zipNames(zr)
	for _, want := range []string{"photos/b.jpg", "photos/2024/a.jpg"} {
		if !hasName(names, want) {
			t.Fatalf("missing %q in %v", want, names)
		}
	}
	if got := readEntry(t, zr, "photos/2024/a.jpg"); got != "A" {
		t.Fatalf("content = %q", got)
	}
}

// Selecting inside a subfolder rebases the entries onto that folder, so the
// archive does not arrive wrapped in three levels of directories nobody asked
// for.
func TestZipRebasesOntoCurrentDir(t *testing.T) {
	_, base, root, share := newTestServer(t)
	if err := os.MkdirAll(filepath.Join(root, "music"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "music", "a.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	zr := readZip(t, base, url.Values{
		"share": {share},
		"dir":   {"music"},
		"paths": {"music/a.mp3"},
	})
	if got := zipNames(zr); len(got) != 1 || got[0] != "a.mp3" {
		t.Fatalf("entries = %v", got)
	}
}

func TestZipRejectsEscape(t *testing.T) {
	_, base, _, share := newTestServer(t)

	for _, bad := range []string{"../secret", "a/../../secret", "..", "a/b/../../../secret"} {
		resp := postZip(t, base, url.Values{"share": {share}, "paths": {bad}})
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("%q: status = %d, want 403", bad, resp.StatusCode)
		}
	}

	// A leading slash is share-relative, not absolute: the same rule
	// handleDownload applies. It must land inside the share, so an absent
	// target is a 404 rather than a peek at the host's filesystem.
	resp := postZip(t, base, url.Values{"share": {share}, "paths": {"/etc/passwd"}})
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("absolute path: status = %d, want 404", resp.StatusCode)
	}
}

func TestZipRejectsMissingSelection(t *testing.T) {
	_, base, _, share := newTestServer(t)

	resp := postZip(t, base, url.Values{"share": {share}})
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// The archive must not contain what the listing hides, otherwise a filtered
// folder reappears the moment it is packaged.
func TestZipHonoursFilters(t *testing.T) {
	s, base, root, share := newTestServer(t)
	s.SetFilter("*.tmp")
	s.SetViewOptions(false, false)

	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"keep.txt", "junk.tmp", ".env"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	names := zipNames(readZip(t, base, url.Values{"share": {share}, "paths": {"proj"}}))
	if !hasName(names, "proj/keep.txt") {
		t.Fatalf("keep.txt missing from %v", names)
	}
	for _, unwanted := range []string{"proj/junk.tmp", "proj/.env", "proj/.git/config"} {
		if hasName(names, unwanted) {
			t.Fatalf("%q should have been filtered out of %v", unwanted, names)
		}
	}
}

func TestZipSkipsSymlinks(t *testing.T) {
	_, base, root, share := newTestServer(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "box")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "leak.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	names := zipNames(readZip(t, base, url.Values{"share": {share}, "paths": {"box"}}))
	if hasName(names, "box/leak.txt") {
		t.Fatalf("symlink escaped into the archive: %v", names)
	}
	if !hasName(names, "box/ok.txt") {
		t.Fatalf("regular file missing: %v", names)
	}
}

// A file deleted between the listing and the download must not sink the batch:
// in a browser the error response would replace the page.
func TestZipSkipsVanishedEntry(t *testing.T) {
	_, base, root, share := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "here.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	zr := readZip(t, base, url.Values{
		"share": {share},
		"paths": {"here.txt", "gone.txt"},
	})
	names := zipNames(zr)
	if len(names) != 1 || names[0] != "here.txt" {
		t.Fatalf("entries = %v", names)
	}
}

func TestZipArchiveName(t *testing.T) {
	cases := []struct {
		name  string
		items []zipItem
		dir   string
		share string
		want  string
	}{
		{"single file", []zipItem{{entry: "a.txt"}}, "", "share", "a.txt.zip"},
		{"single folder", []zipItem{{entry: "photos"}}, "", "share", "photos.zip"},
		{"many in folder", []zipItem{{entry: "a"}, {entry: "b"}}, "music", "share", "music.zip"},
		{"many at root", []zipItem{{entry: "a"}, {entry: "b"}}, "", "share", "share.zip"},
	}
	for _, c := range cases {
		if got := zipArchiveName(c.items, c.dir, c.share); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
