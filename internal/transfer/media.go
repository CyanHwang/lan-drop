package transfer

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/CyanHwang/lan-drop/internal/i18n"
)

// Playback has two paths. The first is the obvious one: hand the original file
// to a <video> element and let the browser do the work, which costs nothing and
// keeps seeking exact. It only fails for containers and codecs the browser has
// no decoder for — a phone's HEVC .mov, an .avi from 2004, a .wmv.
//
// The second path exists for exactly that case: ffmpeg re-encodes the stream to
// H.264/AAC and the response is the encoder's pipe, so nothing is written to
// disk and playback starts on the first frames instead of after the whole file.
// It is a fallback, not the default, because it burns CPU on the host and its
// output is a live stream that can only be seeked within what has been sent.
const (
	// transcodeWorkers bounds simultaneous encodes. One viewer is enough for a
	// LAN box that is also serving the transfer itself; the second slot covers
	// a user opening a file on their phone while a desktop tab still holds one.
	transcodeWorkers = 2
	// transcodeWidth is the longest edge of the re-encoded picture. 1280 is
	// sharp on a phone and keeps the host's CPU bill plausible; the source
	// resolution is still reported in the viewer's footer.
	transcodeWidth = 1280
)

// transcodeSlots is deliberately package-level: the server can be stopped and
// restarted, but an encoder that is still draining belongs to the process, not
// to one listener.
var transcodeSlots = make(chan struct{}, transcodeWorkers)

// handleTranscode streams a browser-playable rendition of one video.
//
// The 503/415 answers are part of the contract: the page uses them to decide
// between "the browser cannot play this, download it" and "the host has no
// ffmpeg, so there is nothing to fall back to".
func (s *Server) handleTranscode(w http.ResponseWriter, r *http.Request) {
	lang := s.langFor(r)
	bin := s.ffmpegPath()
	if bin == "" {
		fail(w, http.StatusServiceUnavailable, i18n.T(lang, i18n.WebVideoFallback))
		return
	}

	q := r.URL.Query()
	rel := strings.Trim(q.Get("path"), "/")
	if rel == "" {
		fail(w, http.StatusBadRequest, i18n.T(lang, i18n.ErrMissingPath))
		return
	}
	abs, _, err := s.resolve(lang, q.Get("share"), rel)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if !isVideoName(abs) {
		fail(w, http.StatusUnsupportedMediaType, i18n.T(lang, i18n.WebVideoFallback))
		return
	}

	// Queue rather than reject: a second viewer waits for the first encoder to
	// finish or to be closed, and gives up as soon as its own tab goes away.
	select {
	case transcodeSlots <- struct{}{}:
		defer func() { <-transcodeSlots }()
	case <-r.Context().Done():
		return
	}

	cmd := exec.CommandContext(r.Context(), bin, transcodeArgs(abs)...)
	// A GUI host must not flash a console for each playback.
	cmd.SysProcAttr = quietProc()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Keep stderr buffered only for diagnosis; the pipe still has to be drained
	// or ffmpeg blocks once the buffer fills.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		fail(w, http.StatusServiceUnavailable, i18n.T(lang, i18n.WebVideoFallback))
		return
	}

	// Wait for real bytes before committing to a 200. Writing the header first
	// turns "ffmpeg cannot open this file" into a silent blank player, which is
	// the one failure mode a user cannot diagnose.
	head := make([]byte, 32<<10)
	n, _ := readSome(stdout, head)
	if n == 0 {
		_ = cmd.Wait()
		fail(w, http.StatusUnsupportedMediaType, i18n.T(lang, i18n.WebVideoFallback))
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(head[:n])
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	// From here the client's connection is the deadline: when the viewer closes,
	// the request context is cancelled and CommandContext kills the encoder.
	_, _ = io.Copy(w, stdout)
	_ = cmd.Wait()
}

// readSome returns as soon as at least one byte arrives. A pipe read can return
// (0, nil) while ffmpeg is still opening the container, which is not an error
// and must not be mistaken for one; waiting for a full buffer would only add
// latency to every play.
func readSome(r io.Reader, buf []byte) (int, error) {
	for total := 0; total < len(buf); {
		n, err := r.Read(buf[total:])
		total += n
		if total > 0 {
			return total, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, nil
			}
			return 0, err
		}
	}
	return len(buf), nil
}

// transcodeArgs is the encoder command. It is a separate function because the
// filter string is the part that breaks silently: without the quotes ffmpeg
// reads the comma inside min() as a filter separator.
func transcodeArgs(abs string) []string {
	return []string{
		"-nostdin", "-loglevel", "error",
		// Rotation metadata is honoured by default, so a portrait clip comes
		// out upright.
		"-i", abs,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-vf", "scale='min(" + strconv.Itoa(transcodeWidth) + ",iw)':-2",
		"-pix_fmt", "yuv420p",
		// Opus and FLAC are common in .mkv, and neither plays in Safari; AAC
		// plays everywhere.
		"-c:a", "aac", "-b:a", "128k", "-ac", "2",
		// Fragmented MP4 so the pipe is a valid stream from the first byte
		// instead of an index that only exists at the end.
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	}
}

// TranscodeAvailable reports whether the host can produce playable renditions.
// Exposed to the page as a data attribute so the viewer knows, before it opens
// a file, whether a decode failure is recoverable.
func (s *Server) TranscodeAvailable() bool {
	return s.ffmpegPath() != ""
}
