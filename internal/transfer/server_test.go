package transfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/CyanHwang/lan-drop/internal/i18n"
)

// newTestServer starts a server exposing one temp directory and returns the
// base URL plus that share's id.
func newTestServer(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	root := t.TempDir()
	share := Share{ID: "test", Name: "test", Path: root}
	s := New(nil)
	info, err := s.Start([]Share{share}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, fmt.Sprintf("http://127.0.0.1:%d", info.Port), root, share.ID
}

func do(t *testing.T, method, url string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// doNoRedirect issues a GET and hands back the first response, so a redirect
// can be inspected instead of followed.
func doNoRedirect(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func TestVirtualRootListsShares(t *testing.T) {
	_, base, _, _ := newTestServer(t)

	resp := do(t, "GET", base+"/api/list", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Mode != "roots" {
		t.Fatalf("mode = %q want roots", out.Mode)
	}
	if len(out.Roots) != 1 || out.Roots[0].Name != "test" {
		t.Fatalf("roots = %+v", out.Roots)
	}
	// The host path must not leak to remote clients.
	if strings.Contains(fmt.Sprintf("%+v", out.Roots), "Temp") {
		t.Fatalf("share path leaked: %+v", out.Roots)
	}
}

func TestListEmptyShare(t *testing.T) {
	_, base, _, share := newTestServer(t)

	resp := do(t, "GET", base+"/api/list?share="+share+"&dir=", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var out listResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Mode != "files" {
		t.Fatalf("mode = %q want files", out.Mode)
	}
	if len(out.Entries) != 0 {
		t.Fatalf("entries = %+v", out.Entries)
	}
	if out.Parent != nil {
		t.Fatalf("parent should be nil at share root")
	}
}

func TestUnknownShareRejected(t *testing.T) {
	_, base, _, _ := newTestServer(t)
	resp := do(t, "GET", base+"/api/list?share=nope&dir=", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d want 403", resp.StatusCode)
	}
}

func TestUploadThenDownload(t *testing.T) {
	_, base, root, share := newTestServer(t)

	payload := bytes.Repeat([]byte("lan-drop-"), 90000) // ~0.6 MB
	resp := do(t, "PUT", base+"/api/upload?share="+share+"&dir=&name=sample.bin", bytes.NewReader(payload),
		map[string]string{"X-Offset": "0"})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}

	onDisk, err := os.ReadFile(filepath.Join(root, "sample.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("file mismatch: got %d bytes want %d", len(onDisk), len(payload))
	}

	dl := do(t, "GET", base+"/api/download?share="+share+"&path=sample.bin", nil, nil)
	defer dl.Body.Close()
	if dl.StatusCode != 200 {
		t.Fatalf("download status = %d", dl.StatusCode)
	}
	got, _ := io.ReadAll(dl.Body)
	if !bytes.Equal(got, payload) {
		t.Fatalf("download mismatch: got %d want %d", len(got), len(payload))
	}
	if cd := dl.Header.Get("Content-Disposition"); !strings.Contains(cd, "sample.bin") {
		t.Fatalf("content-disposition = %q", cd)
	}
}

func TestResumeUpload(t *testing.T) {
	_, base, root, share := newTestServer(t)

	payload := make([]byte, 3*1024*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	split := 1024 * 1024

	resp := do(t, "PUT", base+"/api/upload?share="+share+"&dir=&name=big.bin", bytes.NewReader(payload[:split]),
		map[string]string{"X-Offset": "0", "X-Total-Size": strconv.Itoa(len(payload))})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("first leg status = %d", resp.StatusCode)
	}

	pr := do(t, "GET", base+"/api/probe?share="+share+"&dir=&name=big.bin", nil, nil)
	probeBody, _ := io.ReadAll(pr.Body)
	pr.Body.Close()
	if !strings.Contains(string(probeBody), fmt.Sprintf(`"size":%d`, split)) {
		t.Fatalf("probe = %s", probeBody)
	}

	resp2 := do(t, "PUT", base+"/api/upload?share="+share+"&dir=&name=big.bin", bytes.NewReader(payload[split:]),
		map[string]string{"X-Offset": strconv.Itoa(split), "X-Total-Size": strconv.Itoa(len(payload))})
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("second leg status = %d", resp2.StatusCode)
	}

	onDisk, err := os.ReadFile(filepath.Join(root, "big.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("resumed file mismatch: %d vs %d bytes", len(onDisk), len(payload))
	}
}

// Resume data lives in the staging file, never in the published one. A client
// that claims an offset the staging file does not have must be told the truth
// rather than allowed to write into a hole.
func TestResumeOffsetMismatchReturns409(t *testing.T) {
	_, base, root, share := newTestServer(t)

	if err := os.WriteFile(partPath(filepath.Join(root, "partial.bin")), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := do(t, "PUT", base+"/api/upload?share="+share+"&dir=&name=partial.bin", strings.NewReader("xxxxx"),
		map[string]string{"X-Offset": "100"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d want 409", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"size":5`) {
		t.Fatalf("body = %s", body)
	}
}

// A finished file with a different size must not be offered as a resume point:
// the client would append its own bytes to the end of somebody else's file.
func TestPublishedFileIsNotAResumePoint(t *testing.T) {
	_, base, root, share := newTestServer(t)

	if err := os.WriteFile(filepath.Join(root, "other.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := do(t, "GET", base+"/api/probe?share="+share+"&dir=&name=other.bin&total=9", nil, nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"size":0`) {
		t.Fatalf("probe = %s, want a zero resume offset", body)
	}
	if !strings.Contains(string(body), `"complete":false`) {
		t.Fatalf("probe = %s, want complete:false", body)
	}
}

func TestRangeDownload(t *testing.T) {
	_, base, root, share := newTestServer(t)

	data := []byte("0123456789abcdefghij")
	if err := os.WriteFile(filepath.Join(root, "range.txt"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	resp := do(t, "GET", base+"/api/download?share="+share+"&path=range.txt", nil,
		map[string]string{"Range": "bytes=5-9"})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d want 206", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "56789" {
		t.Fatalf("body = %q want %q", got, "56789")
	}
}

func TestPathTraversalRejected(t *testing.T) {
	_, base, _, share := newTestServer(t)

	cases := []string{
		"/api/download?share=" + share + "&path=../../../../etc/passwd",
		"/api/download?share=" + share + "&path=..%2F..%2Fsecret.txt",
		"/api/list?share=" + share + "&dir=../../..",
		"/api/probe?share=" + share + "&dir=../..&name=x.txt",
	}
	for _, c := range cases {
		resp := do(t, "GET", base+c, nil, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s -> %d, expected 403/400", c, resp.StatusCode)
		}
	}

	resp := do(t, "PUT", base+"/api/upload?share="+share+"&dir=&name=..%2F..%2Fevil.txt", strings.NewReader("x"),
		map[string]string{"X-Offset": "0"})
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("upload with dirty name -> %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "evil.txt")); err == nil {
		t.Fatal("file escaped the share root")
	}
}

func TestMkdirListDelete(t *testing.T) {
	_, base, root, share := newTestServer(t)

	resp := do(t, "POST", base+"/api/mkdir?share="+share+"&dir=&name=photos", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("mkdir -> %d", resp.StatusCode)
	}
	if st, err := os.Stat(filepath.Join(root, "photos")); err != nil || !st.IsDir() {
		t.Fatal("directory not created")
	}

	lr := do(t, "GET", base+"/api/list?share="+share+"&dir=", nil, nil)
	body, _ := io.ReadAll(lr.Body)
	lr.Body.Close()
	if !strings.Contains(string(body), `"name":"photos"`) || !strings.Contains(string(body), `"isDir":true`) {
		t.Fatalf("list = %s", body)
	}

	dr := do(t, "DELETE", base+"/api/file?share="+share+"&path=photos", nil, nil)
	dr.Body.Close()
	if dr.StatusCode != 200 {
		t.Fatalf("delete -> %d", dr.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "photos")); !os.IsNotExist(err) {
		t.Fatal("directory still exists")
	}
}

func TestRejectsDeletingShareRoot(t *testing.T) {
	_, base, _, share := newTestServer(t)
	resp := do(t, "DELETE", base+"/api/file?share="+share+"&path=", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", resp.StatusCode)
	}
}

// Two shares must stay isolated: a file in one is unreachable through the other.
func TestSharesAreIsolated(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	if err := os.WriteFile(filepath.Join(rootA, "only-in-a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "only-in-b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(nil)
	info, err := s.Start([]Share{
		{ID: "a", Name: "A", Path: rootA},
		{ID: "b", Name: "B", Path: rootB},
	}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	// Listing the virtual root shows both, in order.
	resp := do(t, "GET", base+"/api/list", nil, nil)
	var out listResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if len(out.Roots) != 2 || out.Roots[0].Name != "A" || out.Roots[1].Name != "B" {
		t.Fatalf("roots = %+v", out.Roots)
	}

	// A sees its own file and not B's.
	la := do(t, "GET", base+"/api/list?share=a&dir=", nil, nil)
	bodyA, _ := io.ReadAll(la.Body)
	la.Body.Close()
	if !strings.Contains(string(bodyA), "only-in-a.txt") || strings.Contains(string(bodyA), "only-in-b.txt") {
		t.Fatalf("share a listing = %s", bodyA)
	}

	// Fetching B's filename through share A must 404, not leak across.
	cross := do(t, "GET", base+"/api/download?share=a&path=only-in-b.txt", nil, nil)
	cross.Body.Close()
	if cross.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-share fetch -> %d want 404", cross.StatusCode)
	}
}

func TestStartRequiresShares(t *testing.T) {
	s := New(nil)
	if _, err := s.Start(nil, 0); err == nil {
		t.Fatal("expected error when starting with no shares")
	}
	_ = s.Stop()
}

// The page and /api/info must agree on one build id. It is how an already-open
// tab notices that the binary underneath it was replaced and reloads itself.
func TestPageCarriesBuildStamp(t *testing.T) {
	_, base, _, _ := newTestServer(t)

	page := do(t, "GET", base+"/", nil, nil)
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !bytes.Contains(body, []byte(pageBuild)) {
		t.Fatalf("page does not carry build id %q", pageBuild)
	}

	info := do(t, "GET", base+"/api/info", nil, nil)
	var out map[string]any
	_ = json.NewDecoder(info.Body).Decode(&out)
	info.Body.Close()
	if out["build"] != pageBuild {
		t.Fatalf("info build = %v, want %q", out["build"], pageBuild)
	}
}

// getPage fetches a page and returns its body plus the final URL, following
// redirects so a single-share server lands on the listing.
func getPage(t *testing.T, url string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	resp := do(t, "GET", url, nil, headers)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// The listing is rendered by the server: the names must already be in the
// HTML, with no second request needed to fill it in.
func TestPageRendersListing(t *testing.T) {
	_, base, root, share := newTestServer(t)
	if err := os.MkdirAll(filepath.Join(root, "photos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, body := getPage(t, base+"/", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Request.URL.Path != "/s/"+share+"/" {
		t.Fatalf("single share should redirect to its listing, got %s", resp.Request.URL.Path)
	}
	for _, want := range []string{"notes.txt", "photos", "/s/" + share + "/photos/", "app.css?v="} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	if strings.Contains(body, "Connecting") {
		t.Fatal("page still ships the client-side loading placeholder")
	}
}

// A file name is attacker-controlled text. It has to come back escaped in the
// body, and a directory name has to be percent-encoded in its link, or one
// uploaded entry owns the page.
func TestPageEscapesNames(t *testing.T) {
	_, base, root, share := newTestServer(t)
	// Angle brackets are illegal in a Windows file name, so the interesting
	// cases are the characters a name may actually contain.
	for _, n := range []string{"tom & jerry.txt", "it's.txt"} {
		if err := os.WriteFile(filepath.Join(root, n), []byte("x"), 0o644); err != nil {
			t.Fatalf("create %q: %v", n, err)
		}
	}
	// A directory carries a real link, so its name lands in an href.
	if err := os.Mkdir(filepath.Join(root, "a b#c%d"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, body := getPage(t, base+"/s/"+share+"/", nil)
	if !strings.Contains(body, "tom &amp; jerry.txt") {
		t.Fatal("ampersand in a name was not escaped")
	}
	if !strings.Contains(body, "it&#39;s.txt") {
		t.Fatal("quote in a name was not escaped")
	}
	// Escaped once, not twice: the href must be usable as-is.
	if strings.Contains(body, "%2520") {
		t.Fatal("href was double-encoded")
	}
	if !strings.Contains(body, `href="/s/`+share+`/a%20b%23c%25d/"`) {
		t.Fatalf("directory href is not percent-encoded: %s", body)
	}
}

// The language lives in a cookie and is resolved by the server, so switching
// is a plain link and the page it lands on is already translated.
func TestPageLanguageSwitch(t *testing.T) {
	_, base, _, share := newTestServer(t)

	resp := doNoRedirect(t, base+"/s/"+share+"/?lang=zh-Hans")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("switch status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/s/"+share+"/" {
		t.Fatalf("redirect to %q, want the same path without ?lang", loc)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == langCookie {
			cookie = c.Value
		}
	}
	if cookie != string(i18n.ZHCN) {
		t.Fatalf("lang cookie = %q, want %q", cookie, i18n.ZHCN)
	}

	_, body := getPage(t, base+"/s/"+share+"/", map[string]string{"Cookie": langCookie + "=" + cookie})
	if !strings.Contains(body, `lang="zh-Hans"`) {
		t.Fatal("page did not render in the chosen language")
	}
	if !strings.Contains(body, "上传文件") {
		t.Fatal("page is missing the translated upload label")
	}
}

// A directory name in the URL is untrusted. It must not be able to walk out of
// the share, however it is encoded. (A literal "../../" never reaches the
// server: the HTTP client collapses it before sending.)
func TestPageRejectsTraversal(t *testing.T) {
	_, base, root, share := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		"/s/" + share + "/%2e%2e%2f%2e%2e%2fetc/",
		"/s/" + share + "/..%2f..%2fetc/",
		"/s/" + share + "/sub/%2e%2e%2f%2e%2e%2f%2e%2e%2fetc/",
	} {
		resp, body := getPage(t, base+p, nil)
		if resp.StatusCode == 200 {
			t.Fatalf("%s -> 200, want a refusal", p)
		}
		if strings.Contains(body, "root:x:") {
			t.Fatalf("%s leaked the password file", p)
		}
	}
}

// A directory with more entries than one page renders has to say so and offer
// the rest, instead of shipping a document nobody can parse.
func TestPageTruncatesHugeListing(t *testing.T) {
	_, base, root, share := newTestServer(t)
	for i := 0; i < pageEntryLimit+5; i++ {
		name := fmt.Sprintf("f%04d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, body := getPage(t, base+"/s/"+share+"/", nil)
	if !strings.Contains(body, "?all=1") {
		t.Fatal("truncated page does not offer the full listing")
	}
	if strings.Contains(body, fmt.Sprintf("f%04d.txt", pageEntryLimit)) {
		t.Fatal("page rendered past the limit")
	}

	_, all := getPage(t, base+"/s/"+share+"/?all=1", nil)
	if !strings.Contains(all, fmt.Sprintf("f%04d.txt", pageEntryLimit)) {
		t.Fatal("?all=1 did not render the whole directory")
	}
}

// The stylesheet and script carry the build in their URL, so they can be
// cached forever and still be replaced on the next upgrade.
func TestStaticAssetsAreCacheable(t *testing.T) {
	_, base, _, _ := newTestServer(t)

	for _, p := range []string{"/static/app.css", "/static/app.js"} {
		resp := do(t, "GET", base+p, nil, nil)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s -> %d", p, resp.StatusCode)
		}
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Fatalf("%s Cache-Control = %q", p, cc)
		}
	}
}
