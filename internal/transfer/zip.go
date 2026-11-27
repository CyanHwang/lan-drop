package transfer

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/CyanHwang/lan-drop/internal/i18n"
)

// maxZipPaths bounds how many top-level items one request may select. Every
// item costs a resolve plus a stat, and a hostile client could otherwise queue
// thousands of them before a single byte of archive is written. The browser UI
// mirrors this number so a too-large selection is refused before the form is
// posted: a form response replaces the page, so a server-side rejection would
// cost the user their place in the folder tree.
const maxZipPaths = 20000

// errSkipFile means the entry could not be opened and was left out of the
// archive. It is not fatal: one locked file should not sink a batch download.
var errSkipFile = errors.New("skip file")

// zipItem is one selected file or folder, already resolved against the share.
type zipItem struct {
	abs   string
	entry string
	isDir bool
}

// handleZipDownload streams a ZIP of the selected files and folders.
//
// The archive is written straight into the response body — no temp file, no
// buffer holding it — so the memory cost is one io.Copy buffer plus the zip
// writer's small per-entry state, regardless of how much data is packaged.
//
// The price of streaming is that the response carries no Content-Length, so the
// browser cannot show a percentage and the download cannot be resumed. Single
// files deliberately keep using handleDownload, which retains Range support.
//
// Entries are stored uncompressed. On a LAN the link is the bottleneck, a
// single deflate core would sit well below it, and the bulk of what people
// share (video, images, archives) does not compress anyway.
func (s *Server) handleZipDownload(w http.ResponseWriter, r *http.Request) {
	// A GET keeps the endpoint usable from curl and simple scripts; the browser
	// UI posts a form instead, so selecting thousands of entries does not run
	// into URL length limits.
	if err := r.ParseForm(); err != nil {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrBadPath))
		return
	}

	shareID := r.Form.Get("share")
	dir := strings.Trim(r.Form.Get("dir"), "/")
	rels := r.Form["paths"]

	if len(rels) == 0 {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrNothingSelected))
		return
	}
	if len(rels) > maxZipPaths {
		fail(w, http.StatusBadRequest, i18n.T(s.langFor(r), i18n.ErrTooManyItems, maxZipPaths))
		return
	}

	sh, ok := s.shareByID(shareID)
	if !ok {
		fail(w, http.StatusForbidden, i18n.T(s.langFor(r), i18n.ErrShareNotFound))
		return
	}

	// Resolve and stat everything up front: once the first byte of archive is
	// on the wire there is no way left to return a JSON error, so every reason
	// to refuse must be found before that.
	items := make([]zipItem, 0, len(rels))
	for _, raw := range rels {
		rel := strings.Trim(raw, "/")
		if rel == "" {
			continue
		}
		abs, _, err := s.resolve(s.langFor(r), shareID, rel)
		if err != nil {
			// A path that escapes the share is a bug or an attack, not a race.
			fail(w, http.StatusForbidden, err.Error())
			return
		}
		st, err := os.Stat(abs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Deleted between the listing and the download. Dropping it is
				// kinder than failing the whole batch, which in a browser would
				// replace the page with an error document.
				continue
			}
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, zipItem{abs: abs, entry: zipEntryName(dir, rel), isDir: st.IsDir()})
	}
	if len(items) == 0 {
		fail(w, http.StatusNotFound, i18n.T(s.langFor(r), i18n.ErrFileNotFound))
		return
	}

	name := zipArchiveName(items, dir, sh.Name)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	ip, client := clientInfo(r)
	// Total is unknown: measuring it first would mean walking the whole tree
	// before sending anything. The desktop UI shows bytes moved instead.
	rep := s.newReporter(newID(), name, dir, sh.Name, "down", 0, ip, client)
	rep.tick(0)

	cw := &countWriter{ResponseWriter: w, rep: rep}
	zw := zip.NewWriter(cw)

	err := s.writeZip(r.Context(), zw, items)
	if closeErr := zw.Close(); err == nil && closeErr != nil {
		err = closeErr
	}

	if err != nil {
		// Headers are long gone, so the only signal left for the browser is a
		// truncated body. The desktop UI still gets the reason.
		rep.finish(cw.n, err.Error())
		if !errors.Is(err, context.Canceled) {
			s.publish("server:error", err.Error())
		}
		return
	}
	rep.finish(cw.n, "")
}

func (s *Server) writeZip(ctx context.Context, zw *zip.Writer, items []zipItem) error {
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if it.isDir {
			if err := s.zipTree(ctx, zw, it.abs, it.entry); err != nil {
				return err
			}
			continue
		}
		info, err := os.Stat(it.abs)
		if err != nil {
			continue
		}
		if err := zipAddFile(zw, it.abs, it.entry, info); errors.Is(err, errSkipFile) {
			continue
		} else if err != nil {
			return err
		}
	}
	return nil
}

// zipTree appends every file under one selected folder, keeping the relative
// layout so the archive extracts back into the same shape.
//
// Symlinks are never followed: a link can point outside the share, and a
// directory link can loop forever. The walk root itself is resolved because
// resolve() validates the real path but returns the joined one.
func (s *Server) zipTree(ctx context.Context, zw *zip.Writer, abs, entry string) error {
	root := abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		root = real
	}

	showHidden, showDotfiles := s.ViewOptions()
	filter := s.Filter()

	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory we cannot read is skipped rather than fatal.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if p == root {
			return nil
		}

		name := d.Name()
		// Same two filters the listing applies, so what the user could not see
		// does not reappear inside the archive.
		if !showDotfiles && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if filter.Match(name) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if !showHidden && isHiddenInfo(info) {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}

		err = zipAddFile(zw, p, path.Join(entry, filepath.ToSlash(rel)), info)
		if errors.Is(err, errSkipFile) {
			return nil
		}
		return err
	})
}

// zipAddFile appends one stored file to the archive.
//
// A file that cannot be opened returns errSkipFile and is simply left out.
// Anything that fails after the entry header has been written is returned as a
// real error and must abort the response: a half-written entry makes the whole
// archive unreadable.
func zipAddFile(zw *zip.Writer, abs, entry string, info fs.FileInfo) error {
	f, err := os.Open(abs)
	if err != nil {
		return errSkipFile
	}
	defer f.Close()

	h := &zip.FileHeader{
		Name:     entry,
		Method:   zip.Store,
		Modified: info.ModTime(),
	}
	h.SetMode(info.Mode())

	dst, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	// io.Copy reuses its own 32 KiB buffer; the size the file had when it was
	// stat'd does not have to match what is read, because the zip writer
	// derives the entry sizes from the bytes that actually arrive.
	_, err = io.Copy(dst, f)
	return err
}

// zipEntryName maps a share-relative path onto the name used inside the
// archive. Paths are rebased onto the folder the user was browsing, so picking
// three files in "music/" yields "a.mp3", not "music/a.mp3".
func zipEntryName(dir, rel string) string {
	if dir == "" {
		return rel
	}
	if rest, ok := strings.CutPrefix(rel, dir+"/"); ok {
		return rest
	}
	return path.Base(rel)
}

// zipArchiveName picks a download name: the single selected item when there is
// one, otherwise the folder it came from.
func zipArchiveName(items []zipItem, dir, shareName string) string {
	var candidate string
	switch {
	case len(items) == 1:
		candidate = path.Base(items[0].entry) + ".zip"
	case dir != "":
		candidate = path.Base(dir) + ".zip"
	case shareName != "":
		candidate = shareName + ".zip"
	default:
		candidate = "archive.zip"
	}
	// Share and folder names come from the host's filesystem and may contain
	// characters Windows rejects in a download name.
	if clean, err := sanitizeName(candidate); err == nil {
		return clean
	}
	return "archive.zip"
}
