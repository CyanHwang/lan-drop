package transfer

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Playback falls back to a transcode only for video, and only when the host
// has ffmpeg. Everything else has to be a clean refusal the page can explain,
// not a 200 with an empty body that leaves a blank player on screen.
func TestTranscodeRefusesNonVideo(t *testing.T) {
	_, base, root, share := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := do(t, "GET", base+"/api/media/transcode?share="+share+"&path=notes.txt", nil, nil)
	defer resp.Body.Close()
	// 415 when ffmpeg is present and the file is simply not video; 503 when the
	// host cannot transcode at all. Both mean "download it instead".
	if resp.StatusCode != http.StatusUnsupportedMediaType &&
		resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want a refusal", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type = %q, want JSON so the page can read the reason", ct)
	}
}

// The URL is user input like any other: a path outside the share must not reach
// ffmpeg, whatever the query string says.
func TestTranscodeRefusesTraversal(t *testing.T) {
	_, base, root, share := newTestServer(t)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	resp := do(t, "GET",
		base+"/api/media/transcode?share="+share+"&path=..%2f..%2fsecret.mp4", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("transcoded a path outside the share")
	}
}

// Fragmented MP4 is what makes the pipe playable from the first byte; libx264
// with yuv420p is what makes it playable in Safari as well as Chrome.
func TestTranscodeArgsProduceAFragmentedStream(t *testing.T) {
	got := strings.Join(transcodeArgs("/tmp/clip.mkv"), " ")
	for _, want := range []string{
		"-c:v libx264",
		"-pix_fmt yuv420p",
		"-movflags frag_keyframe+empty_moov+default_base_moof",
		"-f mp4 pipe:1",
		"-c:a aac",
		// The comma inside min() must stay quoted, otherwise ffmpeg reads it as
		// a filter separator and the whole graph fails.
		"scale='min(1280,iw)':-2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcode args are missing %q: %s", want, got)
		}
	}
}
