package transfer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestClientInfo(t *testing.T) {
	cases := []struct {
		remote string
		ua     string
		wantIP string
		want   string
	}{
		{"192.168.1.23:51234", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Version/17.0 Mobile Safari/604.1",
			"192.168.1.23", "iPhone Safari"},
		{"[::ffff:10.0.0.5]:43210", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36",
			"10.0.0.5", "Android Chrome"},
		{"[fe80::1%eth0]:9999", "", "fe80::1", ""},
		{"[::1]:1234", "curl/8.4.0", "::1", "curl"},
		{"192.168.1.9", "", "192.168.1.9", ""}, // no port: fall back to the raw address
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		if c.ua != "" {
			req.Header.Set("User-Agent", c.ua)
		}
		ip, label := clientInfo(req)
		if ip != c.wantIP || label != c.want {
			t.Errorf("clientInfo(%q, %q) = (%q, %q), want (%q, %q)",
				c.remote, c.ua, ip, label, c.wantIP, c.want)
		}
	}
}

func TestClientLabelSpecificBrandsBeforeChrome(t *testing.T) {
	cases := []struct{ ua, want string }{
		{"Mozilla/5.0 (Windows NT 10.0) Chrome/120 Safari/537.36 Edg/120", "Windows Edge"},
		{"Mozilla/5.0 (Linux; Android 13) Chrome/110 Safari/537.36 MicroMessenger/8.0", "Android WeChat"},
		{"Mozilla/5.0 (Linux; Android 13) SamsungBrowser/21 Chrome/110 Safari/537.36", "Android Samsung"},
		{"Mozilla/5.0 (iPhone) AppleWebKit/605 FxiOS/120", "iPhone Firefox"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) Version/17 Safari/605", "macOS Safari"},
		{"Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101 Firefox/121", "Linux Firefox"},
		{"", ""},
	}
	for _, c := range cases {
		if got := clientLabel(c.ua); got != c.want {
			t.Errorf("clientLabel(%q) = %q, want %q", c.ua, got, c.want)
		}
	}
}

// The desktop list receives Progress events, so the peer must survive the
// whole path: request -> reporter -> published event.
func TestProgressIncludesClient(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var events []Progress
	s := New(func(name string, data any) {
		if p, ok := data.(Progress); ok {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}
	})
	if _, err := s.StartWithOptions([]Share{{ID: "test", Name: "test", Path: root}}, StartOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	req := httptest.NewRequest(http.MethodGet, "/api/download?share=test&path=a.txt", nil)
	req.RemoteAddr = "192.168.1.42:51234"
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone) Version/17 Mobile Safari/604.1")
	rec := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200", rec.Code)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("no progress events published")
	}
	last := events[len(events)-1]
	if !last.Done || last.ClientIP != "192.168.1.42" || last.Client != "iPhone Safari" {
		t.Fatalf("last event = %+v, want done with 192.168.1.42 / iPhone Safari", last)
	}
}

// Same path over a real TCP socket, so RemoteAddr is whatever the OS reports
// rather than a hand-written value.
func TestProgressClientOverRealSocket(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b.bin"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var last Progress
	var seen bool
	s := New(func(_ string, data any) {
		if p, ok := data.(Progress); ok {
			mu.Lock()
			last, seen = p, true
			mu.Unlock()
		}
	})
	info, err := s.StartWithOptions([]Share{{ID: "test", Name: "test", Path: root}}, StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Stop() })

	url := "http://127.0.0.1:" + strconv.Itoa(info.Port) + "/api/download?share=test&path=b.bin"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if !seen || !last.Done {
		t.Fatalf("no completion event: %+v", last)
	}
	if last.ClientIP != "127.0.0.1" || last.Client != "Android Chrome" {
		t.Fatalf("event = %+v, want 127.0.0.1 / Android Chrome", last)
	}
}
