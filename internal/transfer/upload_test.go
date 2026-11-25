package transfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func put(t *testing.T, url string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	return do(t, "PUT", url, bytes.NewReader(body), headers)
}

func probeOf(t *testing.T, base, share, name string, total int) map[string]any {
	t.Helper()
	url := fmt.Sprintf("%s/api/probe?share=%s&dir=&name=%s", base, share, name)
	if total > 0 {
		url += "&total=" + strconv.Itoa(total)
	}
	resp := do(t, "GET", url, nil, nil)
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	return out
}

// A file must not appear under its real name until every declared byte has
// arrived. Anything else is a corrupt file that looks finished.
func TestUploadStagesUntilLastChunk(t *testing.T) {
	_, base, root, share := newTestServer(t)

	payload := make([]byte, 3*1024*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	split := 1024 * 1024
	target := filepath.Join(root, "stage.bin")

	first := put(t, base+"/api/upload?share="+share+"&dir=&name=stage.bin", payload[:split],
		map[string]string{"X-Offset": "0", "X-Total-Size": strconv.Itoa(len(payload))})
	var firstBody map[string]any
	_ = json.NewDecoder(first.Body).Decode(&firstBody)
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first leg status = %d", first.StatusCode)
	}
	if firstBody["complete"] != false {
		t.Fatalf("first leg complete = %v, want false", firstBody["complete"])
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("published file exists before the last chunk")
	}
	if st, err := os.Stat(partPath(target)); err != nil || st.Size() != int64(split) {
		t.Fatalf("staging file = %v, %v", st, err)
	}

	p := probeOf(t, base, share, "stage.bin", len(payload))
	if p["complete"] != false || p["size"] != float64(split) {
		t.Fatalf("probe = %+v", p)
	}

	last := put(t, base+"/api/upload?share="+share+"&dir=&name=stage.bin", payload[split:],
		map[string]string{"X-Offset": strconv.Itoa(split), "X-Total-Size": strconv.Itoa(len(payload))})
	last.Body.Close()
	if last.StatusCode != 200 {
		t.Fatalf("last leg status = %d", last.StatusCode)
	}

	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("file mismatch: %d vs %d bytes", len(onDisk), len(payload))
	}
	if _, err := os.Stat(partPath(target)); !os.IsNotExist(err) {
		t.Fatal("staging file survived the rename")
	}
	if _, err := os.Stat(partMetaPath(target)); !os.IsNotExist(err) {
		t.Fatal("staging sidecar survived the rename")
	}
}

// A body longer than the declared file can only come from a client that lied.
// Accepting it would leave a file whose size contradicts its own metadata.
func TestUploadRejectsBodyPastDeclaredSize(t *testing.T) {
	_, base, root, share := newTestServer(t)

	resp := put(t, base+"/api/upload?share="+share+"&dir=&name=over.bin", []byte("0123456789"),
		map[string]string{"X-Offset": "0", "X-Total-Size": "4"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", resp.StatusCode)
	}
	if _, err := os.Stat(partPath(filepath.Join(root, "over.bin"))); !os.IsNotExist(err) {
		t.Fatal("a rejected body still left staging data")
	}
}

// Two files that share a name must never be spliced together. The fingerprint
// is what tells the server the staged bytes are somebody else's.
func TestUploadFingerprintMismatchResetsStaging(t *testing.T) {
	_, base, root, share := newTestServer(t)
	url := base + "/api/upload?share=" + share + "&dir=&name=same.bin"

	first := put(t, url, []byte("aaaaaaaaaa"), map[string]string{
		"X-Offset": "0", "X-Total-Size": "20", "X-File-Size": "20", "X-File-Mtime": "111",
	})
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first leg status = %d", first.StatusCode)
	}

	// Same name and size, different mtime: a different file.
	second := put(t, url, []byte("bbbbbbbbbb"), map[string]string{
		"X-Offset": "10", "X-Total-Size": "20", "X-File-Size": "20", "X-File-Mtime": "222",
	})
	var body map[string]any
	_ = json.NewDecoder(second.Body).Decode(&body)
	second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d want 409", second.StatusCode)
	}
	if body["reset"] != true {
		t.Fatalf("body = %+v, want reset:true", body)
	}
	if _, err := os.Stat(partPath(filepath.Join(root, "same.bin"))); !os.IsNotExist(err) {
		t.Fatal("staging data from the other file was kept")
	}
}

func TestUploadFingerprintMatchResumes(t *testing.T) {
	_, base, root, share := newTestServer(t)
	url := base + "/api/upload?share=" + share + "&dir=&name=match.bin"
	hdr := map[string]string{
		"X-Total-Size": "20", "X-File-Size": "20", "X-File-Mtime": "111",
	}

	first := put(t, url, []byte("aaaaaaaaaa"), merge(hdr, map[string]string{"X-Offset": "0"}))
	first.Body.Close()

	second := put(t, url, []byte("bbbbbbbbbb"), merge(hdr, map[string]string{"X-Offset": "10"}))
	second.Body.Close()
	if second.StatusCode != 200 {
		t.Fatalf("resume status = %d", second.StatusCode)
	}

	onDisk, err := os.ReadFile(filepath.Join(root, "match.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(onDisk) != "aaaaaaaaaabbbbbbbbbb" {
		t.Fatalf("content = %q", onDisk)
	}
}

func merge(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// Two devices pushing the same name at once would interleave their bytes.
// The second writer is refused instead of being allowed to corrupt the first.
func TestUploadRejectsConcurrentWriter(t *testing.T) {
	s, base, root, share := newTestServer(t)

	release, ok := s.locks.acquire(strings.ToLower(filepath.Join(root, "busy.bin")))
	if !ok {
		t.Fatal("could not take the lock")
	}
	defer release()

	resp := put(t, base+"/api/upload?share="+share+"&dir=&name=busy.bin", []byte("x"),
		map[string]string{"X-Offset": "0", "X-Total-Size": "1"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d want 409", resp.StatusCode)
	}
}

func TestProbeReportsCompleteFile(t *testing.T) {
	_, base, root, share := newTestServer(t)

	if err := os.WriteFile(filepath.Join(root, "done.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := probeOf(t, base, share, "done.bin", 5)
	if p["complete"] != true || p["size"] != float64(0) {
		t.Fatalf("probe = %+v", p)
	}
}

// Staging bookkeeping is not user data: it must not show up as something the
// user can open, download or mistake for a real file.
func TestListHidesStagingArtifacts(t *testing.T) {
	_, base, root, share := newTestServer(t)

	for _, name := range []string{"real.bin", "real.bin.part", "real.bin.part.meta"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resp := do(t, "GET", base+"/api/list?share="+share+"&dir=", nil, nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"name":"real.bin"`) {
		t.Fatalf("listing lost the real file: %s", body)
	}
	if strings.Contains(string(body), ".part") {
		t.Fatalf("listing leaked staging artifacts: %s", body)
	}
}

func TestDeleteRemovesStagingLeftovers(t *testing.T) {
	_, base, root, share := newTestServer(t)

	target := filepath.Join(root, "gone.bin")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partPath(target), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partMetaPath(target), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := do(t, "DELETE", base+"/api/file?share="+share+"&path=gone.bin", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if _, err := os.Stat(partPath(target)); !os.IsNotExist(err) {
		t.Fatal("staging file outlived its target")
	}
	if _, err := os.Stat(partMetaPath(target)); !os.IsNotExist(err) {
		t.Fatal("staging sidecar outlived its target")
	}
}

// A zero-byte file is a legitimate upload, not a no-op.
func TestZeroByteUploadPublishes(t *testing.T) {
	_, base, root, share := newTestServer(t)

	resp := put(t, base+"/api/upload?share="+share+"&dir=&name=empty.bin", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	st, err := os.Stat(filepath.Join(root, "empty.bin"))
	if err != nil || st.Size() != 0 {
		t.Fatalf("stat = %v, %v", st, err)
	}
}

// Progress describes the file, not the request. A chunked upload that reported
// per-request bytes would drag the desktop UI back to 0% on every chunk and
// show the chunk size divided by the whole transfer's duration as its speed.
func TestProgressIsAbsoluteAcrossChunks(t *testing.T) {
	var mu sync.Mutex
	var events []Progress
	s := New(func(name string, data any) {
		if name != "transfer:progress" {
			return
		}
		if p, ok := data.(Progress); ok {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}
	})
	// Localhost delivers a megabyte in a few milliseconds, so the default
	// 120 ms throttle would swallow every intermediate tick and this test
	// would pass even with the bug in place.
	s.progressInterval = time.Nanosecond

	root := t.TempDir()
	info, err := s.Start([]Share{{ID: "t", Name: "t", Path: root}}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	const chunk = 1024 * 1024
	payload := make([]byte, 3*chunk)
	url := base + "/api/upload?share=t&dir=&name=prog.bin"
	for off := 0; off < len(payload); off += chunk {
		resp := put(t, url, payload[off:off+chunk], map[string]string{
			"X-Offset":     strconv.Itoa(off),
			"X-Total-Size": strconv.Itoa(len(payload)),
			"X-Upload-Id":  "progress-test-1",
		})
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("chunk at %d -> %d", off, resp.StatusCode)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("no progress events")
	}
	var prev int64 = -1
	for _, e := range events {
		if e.Done {
			continue
		}
		if e.Loaded > int64(len(payload)) {
			t.Fatalf("progress overshot the file: %d > %d", e.Loaded, len(payload))
		}
		if e.Loaded < prev {
			t.Fatalf("progress went backwards: %d after %d", e.Loaded, prev)
		}
		prev = e.Loaded
	}
	if prev <= chunk {
		t.Fatalf("last in-flight progress = %d, want more than one chunk (%d)", prev, chunk)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:                      "0 B",
		999:                    "999 B",
		1024:                   "1.0 KB",
		1536:                   "1.5 KB",
		5 * 1024 * 1024:        "5.0 MB",
		3 * 1024 * 1024 * 1024: "3.0 GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

// Cancelling is an explicit signal, not something the server should have to
// infer from a dead socket. The session remembers it, and a chunk that was
// already on the wire when the user hit cancel must not be written.
func TestCancelMarksUploadSession(t *testing.T) {
	_, base, root, share := newTestServer(t)
	url := base + "/api/upload?share=" + share + "&dir=&name=cancel.bin"

	first := put(t, url, []byte("0123456789"), map[string]string{
		"X-Offset": "0", "X-Total-Size": "100", "X-Upload-Id": "cancel-test-1",
	})
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first chunk status = %d", first.StatusCode)
	}

	c := do(t, "POST", base+"/api/upload/cancel?id=cancel-test-1", nil, nil)
	var cb map[string]any
	_ = json.NewDecoder(c.Body).Decode(&cb)
	c.Body.Close()
	if c.StatusCode != 200 || cb["known"] != true {
		t.Fatalf("cancel = %d %+v, want known:true", c.StatusCode, cb)
	}

	next := put(t, url, []byte("more"), map[string]string{
		"X-Offset": "10", "X-Total-Size": "100", "X-Upload-Id": "cancel-test-1",
	})
	var nb map[string]any
	_ = json.NewDecoder(next.Body).Decode(&nb)
	next.Body.Close()
	if next.StatusCode != http.StatusConflict || nb["cancelled"] != true {
		t.Fatalf("late chunk = %d %+v, want 409 cancelled", next.StatusCode, nb)
	}

	// Cancelling discards: nothing may be left holding disk the user cannot
	// even see in the listing.
	if _, err := os.Stat(partPath(filepath.Join(root, "cancel.bin"))); !os.IsNotExist(err) {
		t.Fatal("staged file survived the cancellation")
	}
	if _, err := os.Stat(partMetaPath(filepath.Join(root, "cancel.bin"))); !os.IsNotExist(err) {
		t.Fatal("staging sidecar survived the cancellation")
	}
}

func TestCancelUnknownUploadIsHarmless(t *testing.T) {
	_, base, _, _ := newTestServer(t)

	resp := do(t, "POST", base+"/api/upload/cancel?id=never-existed", nil, nil)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != 200 || body["known"] != false {
		t.Fatalf("cancel = %d %+v, want 200 known:false", resp.StatusCode, body)
	}
}

func TestCancelRequiresId(t *testing.T) {
	_, base, _, _ := newTestServer(t)

	resp := do(t, "POST", base+"/api/upload/cancel", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d want 400", resp.StatusCode)
	}
}

// Stopping the service closes the socket under an in-flight upload. At the
// syscall level that is indistinguishable from the client hanging up, and the
// desktop used to blame the client for it. The shutdown flag is what makes the
// difference visible.
func TestStopReportsStoppedNotClientGone(t *testing.T) {
	var mu sync.Mutex
	var events []Progress
	s := New(func(name string, data any) {
		if p, ok := data.(Progress); ok {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}
	})
	s.progressInterval = time.Nanosecond

	root := t.TempDir()
	info, err := s.Start([]Share{{ID: "t", Name: "t", Path: root}}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	// A body that stops halfway: the server is left blocked on a read, which
	// is exactly the state a real upload is in when the service is stopped.
	pr, pw := io.Pipe()
	req, err := http.NewRequest("PUT", base+"/api/upload?share=t&dir=&name=stop.bin", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 100
	req.Header.Set("X-Offset", "0")
	req.Header.Set("X-Total-Size", "100")
	req.Header.Set("X-Upload-Id", "stop-test-1")
	go func() { _, _ = pw.Write([]byte("0123456789")) }()
	go func() { _, _ = http.DefaultClient.Do(req) }()

	// Wait until the handler is actually running before pulling the plug.
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	_ = pw.Close()

	mu.Lock()
	defer mu.Unlock()
	var last *Progress
	for i := range events {
		if events[i].Done {
			last = &events[i]
		}
	}
	if last == nil {
		t.Fatal("no final progress event")
	}
	if !last.Cancelled {
		t.Fatalf("cancelled = false, error = %q", last.Error)
	}
	if !strings.Contains(last.Error, "service stopped") {
		t.Fatalf("error = %q, want the service-stopped message", last.Error)
	}
	if last.Loaded != 10 {
		t.Fatalf("loaded = %d, want the 10 bytes that actually landed", last.Loaded)
	}
}

// Cancelling while a chunk is in flight: the client announces the cancel, then
// drops the socket. The final event must say "cancelled", not "the client
// disconnected" — those two look identical at the TCP layer.
func TestCancelDuringUploadReportsCancelled(t *testing.T) {
	var mu sync.Mutex
	var events []Progress
	s := New(func(name string, data any) {
		if p, ok := data.(Progress); ok {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}
	})
	s.progressInterval = time.Nanosecond

	root := t.TempDir()
	info, err := s.Start([]Share{{ID: "t", Name: "t", Path: root}}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	// Half a body, then a stall: the server is left waiting for more bytes,
	// which is exactly where a real transfer sits when the user cancels.
	pr, pw := io.Pipe()
	req, err := http.NewRequest("PUT", base+"/api/upload?share=t&dir=&name=cancel2.bin", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 100
	req.Header.Set("X-Offset", "0")
	req.Header.Set("X-Total-Size", "100")
	req.Header.Set("X-Upload-Id", "cancel-inflight")
	go func() { _, _ = pw.Write([]byte("0123456789")) }()
	go func() { _, _ = http.DefaultClient.Do(req) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel, err := http.Post(base+"/api/upload/cancel?id=cancel-inflight", "text/plain", nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	cancel.Body.Close()

	_ = pw.CloseWithError(io.ErrClosedPipe)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	var last *Progress
	for i := range events {
		if events[i].Done {
			last = &events[i]
		}
	}
	if last == nil {
		t.Fatal("no final progress event")
	}
	if !last.Cancelled {
		t.Fatalf("cancelled = false, error = %q", last.Error)
	}
	// The row already reads "cancelled"; the event must not repeat it.
	if last.Error != "" {
		t.Fatalf("error = %q, want an empty reason for a user cancellation", last.Error)
	}
}

// The reported bug: cancelling while no request is in flight left the desktop
// row frozen at its last "running" snapshot. The upload handler only reports a
// final state when a request dies, and between two chunks there is nothing to
// die — so the cancel endpoint has to close the row itself.
func TestCancelBetweenChunksClosesTheRow(t *testing.T) {
	var mu sync.Mutex
	var events []Progress
	s := New(func(name string, data any) {
		if p, ok := data.(Progress); ok {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}
	})
	s.progressInterval = time.Nanosecond

	root := t.TempDir()
	info, err := s.Start([]Share{{ID: "t", Name: "t", Path: root}}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	const chunk = 1024 * 1024
	payload := make([]byte, 4*chunk)
	url := base + "/api/upload?share=t&dir=&name=between.bin"

	first := put(t, url, payload[:chunk], map[string]string{
		"X-Offset":     "0",
		"X-Total-Size": strconv.Itoa(len(payload)),
		"X-Upload-Id":  "between-1",
	})
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first chunk status = %d", first.StatusCode)
	}

	// The request has completed: the client is between chunks, which is where
	// the old code left the row spinning.
	c := do(t, "POST", base+"/api/upload/cancel?id=between-1", nil, nil)
	c.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	var last *Progress
	for i := range events {
		if events[i].Done {
			last = &events[i]
		}
	}
	if last == nil {
		t.Fatal("cancel produced no final event")
	}
	if !last.Cancelled {
		t.Fatalf("cancelled = false, error = %q", last.Error)
	}
	if last.Name != "between.bin" {
		t.Fatalf("name = %q, want the transfer to be identifiable", last.Name)
	}
	if last.Total != int64(len(payload)) {
		t.Fatalf("total = %d, want %d", last.Total, len(payload))
	}
	if last.Loaded != chunk {
		t.Fatalf("loaded = %d, want the %d bytes actually on disk", last.Loaded, chunk)
	}
}

// Same hole as cancelling between chunks, but triggered by the stop button: a
// transfer with no request in flight gets no error, so nothing would close its
// row. Shutting down has to sweep the open sessions itself.
func TestStopClosesIdleUploadRows(t *testing.T) {
	var mu sync.Mutex
	var events []Progress
	s := New(func(name string, data any) {
		if p, ok := data.(Progress); ok {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}
	})
	s.progressInterval = time.Nanosecond

	root := t.TempDir()
	info, err := s.Start([]Share{{ID: "t", Name: "t", Path: root}}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	const chunk = 1024 * 1024
	payload := make([]byte, 4*chunk)
	first := put(t, base+"/api/upload?share=t&dir=&name=idle.bin", payload[:chunk], map[string]string{
		"X-Offset":     "0",
		"X-Total-Size": strconv.Itoa(len(payload)),
		"X-Upload-Id":  "idle-1",
	})
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first chunk status = %d", first.StatusCode)
	}

	// Nothing is in flight when the service stops.
	if err := s.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var last *Progress
	for i := range events {
		if events[i].Done {
			last = &events[i]
		}
	}
	if last == nil {
		t.Fatal("stopping produced no final event for the open transfer")
	}
	if !last.Cancelled {
		t.Fatalf("cancelled = false, error = %q", last.Error)
	}
	if !strings.Contains(last.Error, "service stopped") {
		t.Fatalf("error = %q, want the service-stopped message", last.Error)
	}
	if last.Loaded != chunk {
		t.Fatalf("loaded = %d, want %d", last.Loaded, chunk)
	}
}

// Abandoned staging files are invisible in the listing, so the sweeper is the
// only thing that ever reclaims their disk.
func TestRemoveStaleParts(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old.bin.part")
	oldMeta := filepath.Join(root, "old.bin.part.meta")
	fresh := filepath.Join(root, "fresh.bin.part")
	real := filepath.Join(root, "keep.bin")
	for _, p := range []string{old, oldMeta, fresh, real} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	for _, p := range []string{old, oldMeta} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	if n := removeStaleParts(root, time.Now().Add(-partTTL)); n != 2 {
		t.Fatalf("removed %d files, want 2", n)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a recent staging file was removed")
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatal("a real file was removed")
	}
}

// A chunk whose bytes do not match the checksum the client sent must be rolled
// back and retried, never written and later published as a finished file.
func TestUploadRejectsCorruptChunk(t *testing.T) {
	_, base, root, share := newTestServer(t)
	url := base + "/api/upload?share=" + share + "&dir=&name=crc.bin"
	payload := []byte("0123456789")
	total := strconv.Itoa(len(payload))
	target := filepath.Join(root, "crc.bin")

	crcOf := func(b []byte) string {
		return strconv.FormatUint(uint64(crc32.ChecksumIEEE(b)), 10)
	}

	first := put(t, url, payload[:5], map[string]string{
		"X-Offset": "0", "X-Total-Size": total, "X-Chunk-CRC32": crcOf(payload[:5]),
	})
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("first chunk status = %d", first.StatusCode)
	}

	bad := put(t, url, payload[5:], map[string]string{
		"X-Offset": "5", "X-Total-Size": total, "X-Chunk-CRC32": "1",
	})
	var body map[string]any
	_ = json.NewDecoder(bad.Body).Decode(&body)
	bad.Body.Close()
	if bad.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("corrupt chunk = %d, want 422", bad.StatusCode)
	}
	if body["size"] != float64(5) {
		t.Fatalf("rollback size = %v, want 5", body["size"])
	}
	// Nothing may be published, and the staged file must be back at the last
	// good boundary rather than holding the corrupt tail.
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a corrupt chunk published the file")
	}
	if st, err := os.Stat(partPath(target)); err != nil || st.Size() != 5 {
		t.Fatalf("staged file = %v, %v; want 5 bytes", st, err)
	}

	retry := put(t, url, payload[5:], map[string]string{
		"X-Offset": "5", "X-Total-Size": total, "X-Chunk-CRC32": crcOf(payload[5:]),
	})
	retry.Body.Close()
	if retry.StatusCode != 200 {
		t.Fatalf("retry status = %d", retry.StatusCode)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("content = %q, want %q", onDisk, payload)
	}
}

// The nastier half of the cancel race: the cancel lands while a chunk is still
// being written, and that chunk then finishes normally. Its progress update
// used to reopen the row the cancel had just closed, leaving the desktop stuck
// at whatever percentage the chunk reached.
func TestCancelMidChunkDoesNotReopenTheRow(t *testing.T) {
	var mu sync.Mutex
	var events []Progress
	s := New(func(name string, data any) {
		if p, ok := data.(Progress); ok {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}
	})
	s.progressInterval = time.Nanosecond

	root := t.TempDir()
	info, err := s.Start([]Share{{ID: "t", Name: "t", Path: root}}, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d", info.Port)

	const total = 400
	const half = 200
	pr, pw := io.Pipe()
	req, err := http.NewRequest("PUT", base+"/api/upload?share=t&dir=&name=race.bin", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = total
	req.Header.Set("X-Offset", "0")
	req.Header.Set("X-Total-Size", strconv.Itoa(total))
	req.Header.Set("X-Upload-Id", "race-test-1")

	proceed := make(chan struct{})
	go func() {
		_, _ = pw.Write(make([]byte, half/2))
		<-proceed
		_, _ = pw.Write(make([]byte, half/2))
		_ = pw.Close()
	}()
	go func() { _, _ = http.DefaultClient.Do(req) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("upload never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel while the request is still open, then let the chunk finish.
	cancel, err := http.Post(base+"/api/upload/cancel?id=race-test-1", "text/plain", nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	cancel.Body.Close()
	close(proceed)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	last := events[len(events)-1]
	if !last.Done || !last.Cancelled {
		t.Fatalf("last event = done:%v cancelled:%v loaded:%d, want a closed cancellation",
			last.Done, last.Cancelled, last.Loaded)
	}
	if last.Loaded != half {
		t.Fatalf("loaded = %d, want the %d bytes that actually landed", last.Loaded, half)
	}
	// Nothing may follow the cancellation: that is what reopened the row.
	seenDone := false
	for _, e := range events {
		if e.Done {
			seenDone = true
		} else if seenDone {
			t.Fatalf("a running update (%d%%) arrived after the transfer was closed", e.Loaded)
		}
	}
}

// The other half of the rule: an unexpected failure keeps the staged bytes, so
// a later attempt can finish the job. This mirrors what the browser actually
// does — probe, read the byte count, continue from there with a new task.
func TestFailedUploadResumesAndCompletes(t *testing.T) {
	_, base, root, share := newTestServer(t)
	url := base + "/api/upload?share=" + share + "&dir=&name=resume.bin"
	target := filepath.Join(root, "resume.bin")

	const chunk = 1024 * 1024
	payload := make([]byte, 3*chunk)
	for i := range payload {
		payload[i] = byte(i*7 + 13)
	}
	total := strconv.Itoa(len(payload))
	fp := map[string]string{
		"X-Total-Size": total, "X-File-Size": total, "X-File-Mtime": "1700000000",
	}
	with := func(offset int, extra map[string]string) map[string]string {
		out := map[string]string{"X-Offset": strconv.Itoa(offset)}
		for k, v := range fp {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	// Two chunks land, then the link dies. Nothing cancels or stops: this is
	// the "keep it" case.
	for i := 0; i < 2; i++ {
		off := i * chunk
		resp := put(t, url, payload[off:off+chunk], with(off, map[string]string{"X-Upload-Id": "first-run-1"}))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("chunk %d -> %d", i, resp.StatusCode)
		}
	}
	if st, err := os.Stat(partPath(target)); err != nil || st.Size() != 2*chunk {
		t.Fatalf("staged file = %v, %v; want 2 MiB kept after the failure", st, err)
	}

	// A new attempt, exactly like re-picking the file in the browser.
	p := probeOf(t, base, share, "resume.bin", len(payload))
	if p["size"] != float64(2*chunk) || p["complete"] != false {
		t.Fatalf("probe = %+v, want to resume from 2 MiB", p)
	}

	rest := put(t, url, payload[2*chunk:], with(2*chunk, map[string]string{"X-Upload-Id": "second-run-2"}))
	rest.Body.Close()
	if rest.StatusCode != 200 {
		t.Fatalf("resume chunk -> %d", rest.StatusCode)
	}

	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("resumed file differs from the source (%d vs %d bytes)", len(onDisk), len(payload))
	}
	if _, err := os.Stat(partPath(target)); !os.IsNotExist(err) {
		t.Fatal("staging file survived the completed upload")
	}
}
