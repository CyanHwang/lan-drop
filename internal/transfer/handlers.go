package transfer

import (
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CyanHwang/lan-drop/internal/i18n"
)

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := s.Info()
	writeJSON(w, http.StatusOK, map[string]any{
		"running":  info.Running,
		"port":     info.Port,
		"count":    len(info.Shares),
		"readOnly": info.ReadOnly,
		"lang":     string(s.langFor(r)),
		// The page compares this with its own id and reloads itself when a new
		// binary is deployed under an already-open tab.
		"build": pageBuild,
	})
}

// rootEntry is the LAN-facing view of a share. The absolute path is deliberately
// omitted: remote devices have no business knowing the host's filesystem layout.
type rootEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type listResponse struct {
	Mode      string      `json:"mode"`
	Roots     []rootEntry `json:"roots,omitempty"`
	Share     string      `json:"share,omitempty"`
	ShareName string      `json:"shareName,omitempty"`
	Dir       string      `json:"dir"`
	Parent    *string     `json:"parent"`
	Entries   []Entry     `json:"entries"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	shareID := q.Get("share")
	lang := s.langFor(r)

	// No share selected: the virtual root lists every shared folder.
	if shareID == "" {
		s.mu.RLock()
		roots := make([]rootEntry, 0, len(s.shares))
		for _, sh := range s.shares {
			roots = append(roots, rootEntry{ID: sh.ID, Name: sh.Name})
		}
		s.mu.RUnlock()
		writeJSON(w, http.StatusOK, listResponse{
			Mode:    "roots",
			Roots:   roots,
			Entries: []Entry{},
		})
		return
	}

	dir := strings.Trim(q.Get("dir"), "/")
	entries, sh, status, err := s.listDir(lang, shareID, dir)
	if err != nil {
		fail(w, status, err.Error())
		return
	}

	var parent *string
	if dir != "" {
		p := path.Dir(dir)
		if p == "." {
			p = ""
		}
		parent = &p
	}

	writeJSON(w, http.StatusOK, listResponse{
		Mode:      "files",
		Share:     sh.ID,
		ShareName: sh.Name,
		Dir:       dir,
		Parent:    parent,
		Entries:   entries,
	})
}

// handleProbe tells a client where to resume. The number it reports comes from
// the staging file, never from the published one: a finished file with a
// different size must not be mistaken for a partial upload, and a partial
// upload must never be visible under the real name.
//
// `total` is optional. When present it lets the answer include `complete`,
// which is what lets a re-upload skip a file that is already fully there.
func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name, err := sanitizeName(q.Get("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrInvalidName))
		return
	}
	dir := strings.Trim(q.Get("dir"), "/")

	abs, _, err := s.resolve(s.langFor(r), q.Get("share"), path.Join(dir, name))
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}

	total, _ := strconv.ParseInt(q.Get("total"), 10, 64)

	staged := int64(0)
	if st, err := os.Stat(partPath(abs)); err == nil && !st.IsDir() {
		staged = st.Size()
	}

	complete := false
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		complete = total <= 0 || st.Size() == total
	}

	writeJSON(w, http.StatusOK, map[string]any{
		// exists/size keep their old meaning for anything driving the API by
		// hand; they now describe the staged bytes.
		"exists":   staged > 0,
		"size":     staged,
		"complete": complete,
	})
}

// countWriter reports download progress without giving up Range support.
type countWriter struct {
	http.ResponseWriter
	n   int64
	rep *reporter
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	if n > 0 {
		c.n += int64(n)
		if c.rep != nil {
			c.rep.tick(c.n)
		}
	}
	return n, err
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	rel := strings.Trim(r.URL.Query().Get("path"), "/")
	if rel == "" {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrMissingPath))
		return
	}

	abs, sh, err := s.resolve(s.langFor(r), r.URL.Query().Get("share"), rel)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fail(w, http.StatusNotFound, i18n.T(s.langFor(r), i18n.ErrFileNotFound))
			return
		}
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.IsDir() {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrTargetIsFolder))
		return
	}

	name := st.Name()
	// SVG with inline disposition would run in the page's origin if someone
	// opened the URL directly. The viewer loads it through <img>, which is
	// sandboxed and unaffected by Content-Disposition, so keep the attachment.
	if r.URL.Query().Get("inline") == "1" && !strings.EqualFold(filepath.Ext(name), ".svg") {
		// The in-page viewer needs the bytes, not a download prompt. The name
		// stays in the header so a "save as" from anywhere still works.
		w.Header().Set("Content-Disposition",
			"inline; filename*=UTF-8''"+url.PathEscape(name))
		if ct := inlineContentType(name); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	} else {
		w.Header().Set("Content-Disposition", contentDisposition(name))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")

	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	ip, client := clientInfo(r)
	rep := s.newReporter(newID(), name, dir, sh.Name, "down", st.Size(), ip, client)
	cw := &countWriter{ResponseWriter: w, rep: rep}

	// ServeContent handles Range, If-Modified-Since and ETag for free, which is
	// what makes resumable downloads and video scrubbing work.
	http.ServeContent(cw, r, name, st.ModTime(), f)

	if cw.n > 0 {
		rep.finish(cw.n, "")
	}
}

// writable rejects mutating requests when the server was started read-only.
// Returns false when the response has already been written.
func (s *Server) writable(w http.ResponseWriter, r *http.Request) bool {
	if s.readOnly.Load() {
		fail(w, http.StatusForbidden, i18n.T(s.langFor(r), i18n.ErrReadOnly))
		return false
	}
	return true
}

// handleUpload accepts one leg of an upload. A leg is either a whole file
// (curl, older clients) or one chunk of a larger one; the client decides by
// how much of the declared total it has sent.
//
// Everything lands in a staging file first and is renamed onto the real name
// only when the byte count matches the declared size. That single rule is what
// keeps a half-transferred file from ever being mistaken for a complete one.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !s.writable(w, r) {
		return
	}
	lang := s.langFor(r)
	q := r.URL.Query()
	dir := strings.Trim(q.Get("dir"), "/")

	name, err := sanitizeName(q.Get("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, i18n.T(lang, i18n.ErrInvalidName))
		return
	}

	abs, sh, err := s.resolve(s.langFor(r), q.Get("share"), path.Join(dir, name))
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	offset, _ := strconv.ParseInt(r.Header.Get("X-Offset"), 10, 64)
	if offset < 0 {
		offset = 0
	}
	total, _ := strconv.ParseInt(r.Header.Get("X-Total-Size"), 10, 64)
	if total <= 0 && r.ContentLength > 0 {
		total = offset + r.ContentLength
	}
	// A body that would run past the declared size means the client's offset
	// and its file disagree. Writing it anyway appends garbage past EOF, and
	// the result would still be named like a valid file.
	if total > 0 && r.ContentLength > 0 && offset+r.ContentLength > total {
		fail(w, http.StatusBadRequest, i18n.T(lang, i18n.ErrUploadRange))
		return
	}

	// One writer per destination. Two devices uploading the same name at once
	// would otherwise interleave their chunks into one unsalvageable file.
	id := uploadID(r)
	ip, client := clientInfo(r)
	release, ok := s.locks.acquire(strings.ToLower(abs))
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": i18n.T(lang, i18n.ErrUploadBusy),
			"size":  0,
			"busy":  true,
		})
		return
	}
	defer release()

	sess := s.uploads.begin(id, offset, uploadMeta{
		name: name, dir: dir, share: sh.Name, abs: abs,
		clientIP: ip, client: client, total: total,
	})
	// A chunk can still be on the wire when the user hits cancel. Dropping it
	// here keeps a cancelled transfer from writing more bytes afterwards.
	if sess.cancelled {
		// The transfer was dropped; a chunk still on the wire must not keep
		// its staged bytes alive.
		removePart(abs)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":     i18n.T(lang, i18n.ErrUploadCancelled, humanBytes(offset)),
			"size":      offset,
			"cancelled": true,
		})
		return
	}

	if offset > 0 {
		// Resuming into bytes left by a different file is the one corruption
		// this protocol can still cause on its own. The client's size/mtime
		// fingerprint decides whether the staged data is really its own.
		if size, mtime, hasFP := fileFingerprint(r); hasFP {
			meta, have := readPartMeta(abs)
			if !have || meta.Size != size || meta.MTime != mtime {
				removePart(abs)
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": i18n.T(lang, i18n.ErrFileChanged),
					"size":  0,
					"reset": true,
				})
				return
			}
		}
	} else if total > 0 {
		// Fail at 0% instead of at 99%: a disk that cannot hold the file will
		// never finish the transfer, and the user pays the whole wait twice.
		if free, err := freeSpace(filepath.Dir(abs)); err == nil && free < uint64(total) {
			fail(w, http.StatusInsufficientStorage,
				i18n.T(lang, i18n.ErrNoSpace, humanBytes(total), humanBytes(int64(free))))
			return
		}
	}

	part := partPath(abs)
	flags := os.O_WRONLY | os.O_CREATE
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	if offset > 0 {
		st, statErr := f.Stat()
		if statErr != nil {
			f.Close()
			fail(w, http.StatusInternalServerError, statErr.Error())
			return
		}
		// The client's idea of how much landed must match the disk, otherwise
		// it would write into a hole. 409 tells it the real size to restart from.
		if st.Size() != offset {
			f.Close()
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": i18n.T(lang, i18n.ErrOffsetMismatch),
				"size":  st.Size(),
			})
			return
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else if size, mtime, hasFP := fileFingerprint(r); hasFP {
		_ = writePartMeta(abs, partMeta{Size: size, MTime: mtime})
	}

	rep := s.newReporter(id, name, dir, sh.Name, "up", total, ip, client)
	// Chunks arrive as separate requests but belong to one transfer. The
	// session keeps one clock and one byte base for all of them, so the desktop
	// UI shows the whole file's progress at the rate this session achieved.
	rep.start = sess.start
	rep.base = sess.base
	rep.tick(offset)

	// The client's CRC covers exactly the bytes of this request. Checking it
	// costs one pass over data we already write, and turns a silent bit flip
	// into a retryable error instead of a corrupt file that looks finished.
	wantCRC, hasCRC := chunkCRC(r)
	var crc hash.Hash32
	var dst io.Writer = f
	if hasCRC {
		crc = crc32.NewIEEE()
		dst = io.MultiWriter(f, crc)
	}

	written, copyErr := copyStream(dst, r.Body, rep, offset)
	final := offset + written
	s.uploads.advance(id, final)

	if copyErr != nil {
		_ = f.Close()
		// Three very different things look identical at the socket: the client
		// hung up, the client cancelled, and we closed the listener. Report the
		// one that actually happened instead of always blaming the network.
		msg, deliberate, discard, userCancel := "", false, false, false
		switch {
		case s.uploads.wasCancelled(id):
			// Cancelled means "throw it away": the user asked for the transfer
			// to stop, not for 400 MB to sit invisible on the disk.
			msg, deliberate, discard, userCancel = i18n.T(lang, i18n.ErrUploadCancelled, humanBytes(final)), true, true, true
		case s.shuttingDown.Load():
			// Stopping the service is deliberate too, so the staged bytes go
			// with it. Only an unexpected failure keeps them for a resume.
			msg, deliberate, discard = i18n.T(lang, i18n.ErrServerStopped, humanBytes(final)), true, true
		default:
			msg = uploadError(lang, copyErr, final)
		}
		if discard {
			removePart(abs)
		}
		switch {
		case userCancel:
			// The row already reads "cancelled"; repeating the reason under it
			// just says the same thing twice.
			rep.finishCancelled(final, "")
		case deliberate:
			rep.finishCancelled(final, msg)
		default:
			rep.finish(final, msg)
		}
		// Usually the client hung up and will never read this, but a stalled
		// but still connected one gets the truth about how much landed — which
		// is exactly what its retry needs to resume from.
		writeJSON(w, http.StatusRequestTimeout, map[string]any{
			"error": msg, "size": final, "cancelled": deliberate,
		})
		return
	}

	if hasCRC && crc.Sum32() != wantCRC {
		// What landed is not what was sent. Roll back to the chunk boundary so
		// the retry overwrites clean bytes instead of appending to garbage.
		_ = f.Truncate(offset)
		_ = f.Close()
		msg := i18n.T(lang, i18n.ErrChunkCorrupt, humanBytes(offset))
		rep.finish(final, msg)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": msg, "size": offset, "corrupt": true,
		})
		return
	}

	// More bytes than declared can only come from a client that lied about its
	// size. The extra tail is unusable, so drop the whole staging file rather
	// than leave a landmine that resumes from the wrong place.
	if total > 0 && final > total {
		_ = f.Close()
		removePart(abs)
		rep.finish(final, i18n.T(lang, i18n.ErrUploadRange))
		fail(w, http.StatusBadRequest, i18n.T(lang, i18n.ErrUploadRange))
		return
	}

	// Not the last leg: keep the bytes staged and report progress only. Marking
	// the transfer done here would light up the desktop UI once per chunk.
	if total > 0 && final < total {
		if err := f.Close(); err != nil {
			rep.finish(final, err.Error())
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		// The cancel may have landed while this chunk was in flight. Its event
		// already closed the row; report the exact byte count instead of
		// reopening it with a running update.
		if s.uploads.wasCancelled(id) {
			removePart(abs)
			rep.finishCancelled(final, "")
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "size": final, "complete": false, "cancelled": true,
			})
			return
		}
		rep.send(final, false, "")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "size": final, "complete": false})
		return
	}

	// Last leg. Flush before the rename so a crash cannot publish a name that
	// points at bytes the disk has not committed yet.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		rep.finish(final, err.Error())
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := f.Close(); err != nil {
		rep.finish(final, err.Error())
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(part, abs); err != nil {
		msg := i18n.T(lang, i18n.ErrFinalize, err)
		rep.finish(final, msg)
		fail(w, http.StatusInternalServerError, msg)
		return
	}
	removePartMeta(abs)
	s.uploads.done(id)
	rep.finish(final, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "size": final, "complete": true})
}

// handleUploadCancel records that the browser dropped a transfer on purpose.
// It has to be a separate request: an aborted XHR cannot carry a message, so
// without this the server can only guess that "the client vanished" — and the
// desktop would show a cancellation as a network failure.
func (s *Server) handleUploadCancel(w http.ResponseWriter, r *http.Request) {
	lang := s.langFor(r)
	id, ok := normalizeUploadID(r.URL.Query().Get("id"))
	if !ok {
		fail(w, http.StatusBadRequest, i18n.T(lang, i18n.ErrMissingId))
		return
	}
	sess, known := s.uploads.cancel(id)
	if !known {
		// Nothing in flight under that id: the transfer already ended, so
		// there is no row left to close.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "known": false})
		return
	}
	// Remove the staged bytes now when nothing holds them open. If a request
	// is still writing, the lock is taken and that handler deletes them on its
	// way out — Windows will not delete a file with an open handle anyway.
	if sess.abs != "" {
		if release, free := s.locks.acquire(strings.ToLower(sess.abs)); free {
			removePart(sess.abs)
			release()
		}
	}
	// Close the row here, not only in the upload handler. The handler can only
	// report when a request dies, and a cancel landing in the gap between two
	// chunks has no request to die — the row would sit at "18 MB/s, 2%"
	// forever. When a request is in flight, its own event follows within
	// milliseconds with the exact byte count and overwrites this one.
	s.publish("transfer:progress", Progress{
		ID:        id,
		Name:      sess.name,
		Dir:       sess.dir,
		Share:     sess.share,
		Direction: "up",
		Loaded:    sess.loaded,
		Total:     sess.total,
		Done:      true,
		Cancelled: true,
		At:        sess.start.UnixMilli(),
		ClientIP:  sess.clientIP,
		Client:    sess.client,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "known": true})
}

// chunkCRC reads the checksum the client computed over this request's body.
// A missing or malformed header simply disables the check: curl and scripts
// have no reason to know about it.
func chunkCRC(r *http.Request) (uint32, bool) {
	v := strings.TrimSpace(r.Header.Get("X-Chunk-CRC32"))
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// fileFingerprint reads the client's description of the file it is sending.
// Size and mtime together are enough to tell two files apart in practice, at
// none of the cost of hashing gigabytes on both ends.
func fileFingerprint(r *http.Request) (size, mtime int64, ok bool) {
	size, sizeErr := strconv.ParseInt(r.Header.Get("X-File-Size"), 10, 64)
	mtime, mtimeErr := strconv.ParseInt(r.Header.Get("X-File-Mtime"), 10, 64)
	if sizeErr != nil || mtimeErr != nil || size <= 0 {
		return 0, 0, false
	}
	return size, mtime, true
}

// uploadID keeps every chunk of one transfer on a single row in the desktop
// activity list. Anything that is not a plain token is replaced, so a hostile
// header cannot inject something the UI would render oddly.
func uploadID(r *http.Request) string {
	if id, ok := normalizeUploadID(r.Header.Get("X-Upload-Id")); ok {
		return id
	}
	return newID()
}

// normalizeUploadID is shared by the upload and cancel endpoints so both agree
// on what a valid id is. If they disagreed, a cancel could look up a key that
// was never written and silently do nothing.
func normalizeUploadID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	if len(id) < 8 || len(id) > 64 {
		return "", false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return "", false
		}
	}
	return id, true
}

// uploadError turns a mid-transfer failure into something a human can act on.
// "wsarecv: An existing connection was forcibly closed by the remote host"
// tells the user nothing except that Windows is unhappy; what they need to
// know is how far the file got.
func uploadError(lang i18n.Lang, err error, written int64) string {
	var opErr *net.OpError
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF) ||
		(errors.As(err, &opErr) && opErr.Op == "read") {
		return i18n.T(lang, i18n.ErrClientGone, humanBytes(written))
	}
	return err.Error()
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	if !s.writable(w, r) {
		return
	}
	q := r.URL.Query()
	dir := strings.Trim(q.Get("dir"), "/")

	name, err := sanitizeName(q.Get("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrInvalidName))
		return
	}

	abs, _, err := s.resolve(s.langFor(r), q.Get("share"), path.Join(dir, name))
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !s.writable(w, r) {
		return
	}
	rel := strings.Trim(r.URL.Query().Get("path"), "/")
	if rel == "" {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrDeleteRoot))
		return
	}

	abs, _, err := s.resolve(s.langFor(r), r.URL.Query().Get("share"), rel)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}

	// os.Remove only deletes files and empty directories — no recursive wipe.
	if err := os.Remove(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fail(w, http.StatusNotFound, i18n.T(s.langFor(r), i18n.ErrNotFound))
			return
		}
		fail(w, http.StatusConflict, err.Error())
		return
	}
	// Clear any staging leftovers too. A hidden .part cannot be removed from
	// the UI, so without this it would sit in the share forever, invisible.
	removePart(abs)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// inlineContentType pins the MIME type of the formats the viewer understands.
// ServeContent guesses from the extension, but a LAN box may not have a mime
// database for .md or the Office types, and nosniff then turns them into
// application/octet-stream. Video matters just as much: a <video> element given
// octet-stream with nosniff refuses to play a file it could decode perfectly.
func inlineContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown":
		return "text/markdown; charset=utf-8"
	case ".txt", ".log", ".csv", ".json":
		return "text/plain; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".ogv":
		return "video/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	}
	return ""
}

func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		ascii, url.PathEscape(name))
}

// clientInfo describes the peer for the activity list. RemoteAddr is used
// rather than X-Forwarded-For: there is no trusted proxy on a LAN, and a
// forwarded header is trivially spoofed. The UA label is only a hint for
// telling two devices apart, never a security decision.
func clientInfo(r *http.Request) (ip, label string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// IPv6 zone suffixes (fe80::1%eth0) are noise in a UI list.
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	if parsed := net.ParseIP(host); parsed != nil {
		if v4 := parsed.To4(); v4 != nil {
			host = v4.String() // ::ffff:192.168.1.5 -> 192.168.1.5
		} else {
			host = parsed.String()
		}
	}
	return host, clientLabel(r.UserAgent())
}

// clientLabel reduces a browser UA to two words, e.g. "iPhone Safari".
// Deliberately approximate: the goal is a recognisable label, not exact
// browser identification.
func clientLabel(ua string) string {
	if ua == "" {
		return ""
	}
	platform := ""
	switch {
	case strings.Contains(ua, "iPhone"):
		platform = "iPhone"
	case strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		platform = "iPad"
	case strings.Contains(ua, "Android"):
		platform = "Android"
	case strings.Contains(ua, "Windows"):
		platform = "Windows"
	case strings.Contains(ua, "CrOS"):
		platform = "Chromebook"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		platform = "macOS"
	case strings.Contains(ua, "Linux"):
		platform = "Linux"
	}

	// Order matters: Edge, Opera, Samsung and WeChat all contain "Chrome",
	// and Chrome itself contains "Safari".
	browser := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"), strings.Contains(ua, "Opera"):
		browser = "Opera"
	case strings.Contains(ua, "MicroMessenger"):
		browser = "WeChat"
	case strings.Contains(ua, "SamsungBrowser"):
		browser = "Samsung"
	case strings.Contains(ua, "Firefox/"), strings.Contains(ua, "FxiOS/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"), strings.Contains(ua, "CriOS/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}

	if platform != "" && browser != "" {
		return platform + " " + browser
	}
	if platform != "" {
		return platform
	}
	if browser != "" {
		return browser
	}
	// curl, download managers, scripts: show the first token, bounded.
	if i := strings.IndexAny(ua, " /\t"); i > 0 {
		ua = ua[:i]
	}
	if len(ua) > 16 {
		ua = ua[:16]
	}
	return ua
}
