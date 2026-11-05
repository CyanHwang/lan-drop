package transfer

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newServerAt starts a server rooted at an explicit directory so tests can
// plant symlinks before the listener comes up.
func newServerAt(t testing.TB, root string, opt StartOptions) (*Server, string) {
	t.Helper()
	s := New(nil)
	info, err := s.StartWithOptions([]Share{{ID: "test", Name: "test", Path: root}}, opt)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, "http://127.0.0.1:" + strconv.Itoa(info.Port)
}

// A symlink inside the share that points outside must not become an escape
// hatch. This is the case the old implementation missed: the upload target
// does not exist yet, so EvalSymlinks on the full path fails and the check was
// silently skipped.
func TestSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	_, base := newServerAt(t, root, StartOptions{})

	cases := []struct {
		name   string
		method string
		url    string
		body   io.Reader
	}{
		{"download through link", "GET", "/api/download?share=test&path=escape/secret.txt", nil},
		{"list through link", "GET", "/api/list?share=test&dir=escape", nil},
		{"probe through link", "GET", "/api/probe?share=test&dir=escape&name=secret.txt", nil},
		{"upload through link", "PUT", "/api/upload?share=test&dir=escape&name=pwned.txt", strings.NewReader("x")},
		{"mkdir through link", "POST", "/api/mkdir?share=test&dir=escape&name=pwned-dir", nil},
		{"delete through link", "DELETE", "/api/file?share=test&path=escape/secret.txt", nil},
	}

	for _, c := range cases {
		resp := do(t, c.method, base+c.url, c.body, map[string]string{"X-Offset": "0"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 403/400", c.name, resp.StatusCode)
		}
	}

	if _, err := os.Stat(filepath.Join(outside, "pwned.txt")); err == nil {
		t.Error("upload escaped the share root")
	}
	if _, err := os.Stat(filepath.Join(outside, "pwned-dir")); err == nil {
		t.Error("mkdir escaped the share root")
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.txt")); err != nil {
		t.Error("delete removed a file outside the share root")
	}
}

// Symlinks that stay inside the share must keep working — the guard should
// block escapes, not symlinks in general.
func TestSymlinkInsideShareAllowed(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	_, base := newServerAt(t, root, StartOptions{})

	resp := do(t, "GET", base+"/api/download?share=test&path=alias/note.txt", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
}

func TestReadOnlyRejectsWrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, base := newServerAt(t, root, StartOptions{ReadOnly: true})

	writes := []struct {
		name   string
		method string
		url    string
		body   io.Reader
	}{
		{"upload", "PUT", "/api/upload?share=test&dir=&name=new.txt", strings.NewReader("x")},
		{"mkdir", "POST", "/api/mkdir?share=test&dir=&name=dir", nil},
		{"delete", "DELETE", "/api/file?share=test&path=keep.txt", nil},
	}
	for _, c := range writes {
		resp := do(t, c.method, base+c.url, c.body, map[string]string{"X-Offset": "0"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", c.name, resp.StatusCode)
		}
	}

	// Reads must still work.
	resp := do(t, "GET", base+"/api/download?share=test&path=keep.txt", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download in read-only mode = %d, want 200", resp.StatusCode)
	}

	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Error("read-only server deleted a file")
	}
}

func TestStrictPortFailsWhenTaken(t *testing.T) {
	root := t.TempDir()
	s1 := New(nil)
	info, err := s1.StartWithOptions([]Share{{ID: "a", Name: "a", Path: root}}, StartOptions{Port: 0, StrictPort: true})
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer s1.Stop()

	s2 := New(nil)
	defer s2.Stop()
	if _, err := s2.StartWithOptions([]Share{{ID: "b", Name: "b", Path: root}}, StartOptions{
		Port:       info.Port,
		StrictPort: true,
	}); err == nil {
		t.Fatal("strict mode should fail on a taken port")
	}

	// Non-strict mode walks forward and still comes up.
	s3 := New(nil)
	defer s3.Stop()
	got, err := s3.StartWithOptions([]Share{{ID: "c", Name: "c", Path: root}}, StartOptions{Port: info.Port})
	if err != nil {
		t.Fatalf("non-strict start: %v", err)
	}
	if got.Port == info.Port {
		t.Fatalf("expected a different port, got %d", got.Port)
	}
}
