package transfer

import (
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"testing"
)

func jfif(density int, units byte) []byte {
	seg := make([]byte, 16)
	copy(seg[2:], "JFIF\x00")
	seg[7], seg[8] = 1, 2 // version 1.2
	seg[9] = units
	binary.BigEndian.PutUint16(seg[10:12], uint16(density))
	binary.BigEndian.PutUint16(seg[12:14], uint16(density))
	binary.BigEndian.PutUint16(seg[0:2], uint16(len(seg)))
	return append([]byte{0xff, 0xd8, 0xff, 0xe0}, seg...)
}

func TestJPEGDPI(t *testing.T) {
	if got := jpegDPI(jfif(300, 1)); got != 300 {
		t.Fatalf("dpi = %d, want 300", got)
	}
	if got := jpegDPI(append(jfif(100, 1), 0xff, 0xda)); got != 100 {
		t.Fatalf("dpi before scan = %d", got)
	}
	if got := jpegDPI(jfif(0, 0)); got != 0 {
		t.Fatalf("unknown units should report no dpi, got %d", got)
	}
	if got := jpegDPI([]byte("not a jpeg")); got != 0 {
		t.Fatalf("garbage reported %d dpi", got)
	}
}

func TestPNG_DPI(t *testing.T) {
	b := make([]byte, 8+12+9+4)
	copy(b, "\x89PNG\r\n\x1a\n")
	binary.BigEndian.PutUint32(b[8:12], 9)
	copy(b[12:16], "pHYs")
	binary.BigEndian.PutUint32(b[16:20], 11811) // ~300 dpi
	binary.BigEndian.PutUint32(b[20:24], 11811)
	b[24] = 1 // metres
	if got := pngDPI(b); got != 300 {
		t.Fatalf("dpi = %d, want 300", got)
	}
}

func TestParseRate(t *testing.T) {
	for in, want := range map[string]float64{
		"30000/1001": 29.97002997002997,
		"25/1":       25,
		"0/0":        0,
		"":           0,
		"garbage":    0,
	} {
		if got := parseRate(in); got != want {
			t.Fatalf("parseRate(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPreviewInfoEndpoint(t *testing.T) {
	_, base, root, _ := newTestServer(t)
	writeTestJPEG(t, filepath.Join(root, "photo.jpg"), 640, 480)

	resp := do(t, "GET", base+"/api/preview/info?share=test&path=photo.jpg", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var meta previewMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if meta.Width != 640 || meta.Height != 480 {
		t.Fatalf("dimensions = %dx%d", meta.Width, meta.Height)
	}
}
