package transfer

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// zeroReader produces an endless stream of zero bytes without allocating, so
// the client side stays at constant memory and the benchmark measures the
// server, not the test harness.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// TestThroughput pushes a large file both ways over loopback and reports MB/s.
// It is skipped in -short mode because it writes ~256 MB to disk.
func TestThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping throughput test in short mode")
	}

	_, base, root, share := newTestServer(t)
	const size = 256 << 20 // 256 MiB

	// --- upload ---
	req, err := http.NewRequest("PUT", base+"/api/upload?share="+share+"&dir=&name=big.bin", io.LimitReader(zeroReader{}, size))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = size
	req.Header.Set("X-Offset", "0")
	req.Header.Set("X-Total-Size", "268435456")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	upDur := time.Since(start)
	if resp.StatusCode != 200 {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}

	st, err := os.Stat(filepath.Join(root, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != size {
		t.Fatalf("size on disk = %d want %d", st.Size(), size)
	}

	// --- download ---
	start = time.Now()
	dl, err := http.Get(base + "/api/download?share=" + share + "&path=big.bin")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	n, err := io.Copy(io.Discard, dl.Body)
	dl.Body.Close()
	if err != nil {
		t.Fatalf("download body: %v", err)
	}
	downDur := time.Since(start)
	if n != size {
		t.Fatalf("downloaded %d want %d", n, size)
	}

	mb := float64(size) / (1 << 20)
	t.Logf("upload   %.1f MB in %s → %.0f MB/s", mb, upDur.Round(time.Millisecond), mb/upDur.Seconds())
	t.Logf("download %.1f MB in %s → %.0f MB/s", mb, downDur.Round(time.Millisecond), mb/downDur.Seconds())
}
