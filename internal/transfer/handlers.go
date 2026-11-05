package transfer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/CyanHwang/lan-drop/internal/i18n"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusNotFound, i18n.T(s.L(), i18n.ErrUnknownEndpoint))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(mobileHTML)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := s.Info()
	writeJSON(w, http.StatusOK, map[string]any{
		"running":  info.Running,
		"port":     info.Port,
		"count":    len(info.Shares),
		"readOnly": info.ReadOnly,
		"lang":     string(s.L()),
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
	abs, sh, err := s.resolve(shareID, dir)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}

	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fail(w, http.StatusNotFound, i18n.T(s.L(), i18n.ErrDirNotFound))
			return
		}
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !st.IsDir() {
		fail(w, http.StatusBadRequest, i18n.T(s.L(), i18n.ErrNotFolder))
		return
	}

	dirents, err := os.ReadDir(abs)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	showHidden, showDotfiles := s.ViewOptions()
	filter := s.Filter()

	entries := make([]Entry, 0, len(dirents))
	for _, de := range dirents {
		info, err := de.Info()
		if err != nil {
			continue
		}
		// Two independent filters: the dot prefix is a naming convention that
		// works everywhere, the hidden attribute only exists on Windows.
		if !showDotfiles && strings.HasPrefix(de.Name(), ".") {
			continue
		}
		if !showHidden && isHiddenInfo(info) {
			continue
		}
		if filter.Match(de.Name()) {
			continue
		}
		// Hide dangling or escaping symlinks rather than exposing them.
		if de.Type()&fs.ModeSymlink != 0 {
			if _, _, err := s.resolve(shareID, path.Join(dir, de.Name())); err != nil {
				continue
			}
		}
		entries = append(entries, Entry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UnixMilli(),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

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

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name, err := sanitizeName(q.Get("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, i18n.T(s.L(), i18n.ErrInvalidName))
		return
	}
	dir := strings.Trim(q.Get("dir"), "/")

	abs, _, err := s.resolve(q.Get("share"), path.Join(dir, name))
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}

	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, http.StatusOK, map[string]any{"exists": false, "size": 0})
			return
		}
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.IsDir() {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false, "size": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exists": true, "size": st.Size()})
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
		fail(w, http.StatusBadRequest, i18n.T(s.L(), i18n.ErrMissingPath))
		return
	}

	abs, sh, err := s.resolve(r.URL.Query().Get("share"), rel)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fail(w, http.StatusNotFound, i18n.T(s.L(), i18n.ErrFileNotFound))
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
		fail(w, http.StatusBadRequest, i18n.T(s.L(), i18n.ErrTargetIsFolder))
		return
	}

	name := st.Name()
	w.Header().Set("Content-Disposition", contentDisposition(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	rep := s.newReporter(newID(), name, dir, sh.Name, "down", st.Size())
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
func (s *Server) writable(w http.ResponseWriter) bool {
	if s.readOnly.Load() {
		fail(w, http.StatusForbidden, i18n.T(s.L(), i18n.ErrReadOnly))
		return false
	}
	return true
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !s.writable(w) {
		return
	}
	q := r.URL.Query()
	dir := strings.Trim(q.Get("dir"), "/")

	name, err := sanitizeName(q.Get("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, i18n.T(s.L(), i18n.ErrInvalidName))
		return
	}

	abs, sh, err := s.resolve(q.Get("share"), path.Join(dir, name))
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

	flags := os.O_WRONLY | os.O_CREATE
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(abs, flags, 0o644)
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
				"error": i18n.T(s.L(), i18n.ErrOffsetMismatch),
				"size":  st.Size(),
			})
			return
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	rep := s.newReporter(newID(), name, dir, sh.Name, "up", total)
	rep.tick(0)

	written, copyErr := copyStream(f, r.Body, rep)
	closeErr := f.Close()

	if copyErr != nil {
		rep.finish(written, copyErr.Error())
		// Client hung up mid-transfer; the partial file stays for resume.
		fail(w, http.StatusRequestTimeout, copyErr.Error())
		return
	}
	if closeErr != nil {
		rep.finish(written, closeErr.Error())
		fail(w, http.StatusInternalServerError, closeErr.Error())
		return
	}

	final := offset + written
	rep.finish(final, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "size": final})
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	if !s.writable(w) {
		return
	}
	q := r.URL.Query()
	dir := strings.Trim(q.Get("dir"), "/")

	name, err := sanitizeName(q.Get("name"))
	if err != nil {
		fail(w, http.StatusBadRequest, i18n.T(s.L(), i18n.ErrInvalidName))
		return
	}

	abs, _, err := s.resolve(q.Get("share"), path.Join(dir, name))
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
	if !s.writable(w) {
		return
	}
	rel := strings.Trim(r.URL.Query().Get("path"), "/")
	if rel == "" {
		fail(w, http.StatusBadRequest, i18n.T(s.L(), i18n.ErrDeleteRoot))
		return
	}

	abs, _, err := s.resolve(r.URL.Query().Get("share"), rel)
	if err != nil {
		fail(w, http.StatusForbidden, err.Error())
		return
	}

	// os.Remove only deletes files and empty directories — no recursive wipe.
	if err := os.Remove(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fail(w, http.StatusNotFound, i18n.T(s.L(), i18n.ErrNotFound))
			return
		}
		fail(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
