package transfer

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CyanHwang/lan-drop/internal/i18n"
)

// The browser UI is rendered by the server from templates embedded in the
// binary. There is no build step and no second process: the same listener that
// serves the API serves the page.
//
//go:embed templates/*.html
var tmplFS embed.FS

//go:embed static
var staticFS embed.FS

// pageAssets is the static file system, rooted where the URLs point.
var pageAssets = func() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}()

var pageTmpl = template.Must(template.New("page").ParseFS(tmplFS, "templates/*.html"))

// pageBuild identifies the rendered UI. The page carries the same id and
// reloads itself when the two differ. Without it a rebuilt binary keeps
// feeding the old script to a tab that was already open, which makes every
// server-side fix look like it did nothing at all.
var pageBuild = func() string {
	h := sha256.New()
	for _, root := range []fs.FS{tmplFS, staticFS} {
		_ = fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			b, err := fs.ReadFile(root, p)
			if err != nil {
				return nil
			}
			h.Write([]byte(p))
			h.Write(b)
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil)[:6])
}()

// pageEntryLimit caps how many rows one page renders. A folder with tens of
// thousands of entries would otherwise ship megabytes of HTML; the rest is one
// click away.
const pageEntryLimit = 1000

// langCookie remembers an explicit language choice for this browser. The
// choice is per-device on purpose: a phone and a desktop on the same LAN may
// well want different languages.
const langCookie = "landrop_lang"

// crumb is one step in the path bar. Sep is true when a separator belongs
// before it, so the template stays free of loop bookkeeping.
type crumb struct {
	Name    string
	Href    string
	Current bool
	Sep     bool
}

type viewEntry struct {
	Name         string
	Rel          string
	Href         string
	DownloadHref string
	// ThumbHref is empty unless previews are on and this file can be decoded.
	// The version in the URL is the file's size and mtime: it exists for the
	// browser's cache, not for the server's.
	ThumbHref  string
	IsDir      bool
	Icon       string
	Sub        string
	Delay      string
	AriaSelect string
}

type viewRoot struct {
	ID    string
	Name  string
	Href  string
	Delay string
}

// pageData is everything the templates read. Labels are pre-resolved rather
// than looked up inside the template: one language decision per request, and
// a missing key shows up in tests instead of on a phone.
type pageData struct {
	LangTag string
	Title   string
	Build   string

	ShareID  string
	Dir      string
	ReadOnly bool
	CanWrite bool
	ShowDock bool

	PathLabel string
	HomeHref  string
	LangHref  string
	LangLabel string
	LangAria  string
	RootChip  string

	HasBack   bool
	BackHref  string
	BackAria  string
	BackLabel string

	Crumbs []crumb

	Mode        string
	Empty       bool
	EmptyText   string
	Roots       []viewRoot
	Entries     []viewEntry
	Truncated   bool
	TruncNote   string
	ShowAllHref string
	ShowAll     string

	ErrorTitle   string
	ErrorMessage string
	GoHome       string

	Upload       string
	UploadFiles  string
	UploadFolder string
	NewFolder    string
	CancelAll    string
	Hide         string
	SelectAll    string
	DownloadZip  string
	ClearPick    string
	Download     string
	Delete       string
	SharedFolder string
	DropHere     string

	Runtime template.JS
}

// runtimeMsgs are the strings the page needs after it has loaded. Everything
// else is baked into the HTML; these describe a transfer in progress, which
// only exists in the browser.
var runtimeMsgs = map[string]i18n.Msg{
	"upload":          i18n.WebUpload,
	"selectAll":       i18n.WebSelectAll,
	"clearAll":        i18n.WebClearAll,
	"selectedCount":   i18n.WebSelectedCount,
	"selectNothing":   i18n.WebSelectNothing,
	"tooManyPicked":   i18n.WebTooManyPicked,
	"zipStarted":      i18n.WebZipStarted,
	"downloadStarted": i18n.WebDownloadStarted,
	"confirmDelete":   i18n.WebConfirmDelete,
	"deleted":         i18n.WebDeleted,
	"deleteFailed":    i18n.WebDeleteFailed,
	"newFolderName":   i18n.WebNewFolderName,
	"created":         i18n.WebCreated,
	"createFailed":    i18n.WebCreateFailed,
	"enterShare":      i18n.WebEnterShare,
	"stalled":         i18n.WebStalled,
	"connLost":        i18n.WebConnLost,
	"cancelled":       i18n.WebCancelled,
	"readFailed":      i18n.WebReadFailed,
	"busy":            i18n.WebBusy,
	"serverDown":      i18n.WebServerDown,
	"retrying":        i18n.WebRetrying,
	"sizeMismatch":    i18n.WebSizeMismatch,
	"resuming":        i18n.WebResuming,
	"filesFailed":     i18n.WebFilesFailed,
	"filesUploaded":   i18n.WebFilesUploaded,
	"uploading":       i18n.WebUploading,
	"uploadCount":     i18n.WebUploadCount,
	"uploadDone":      i18n.WebUploadDone,
	"activeUploads":   i18n.WebActiveUploads,
	"done":            i18n.WebDone,
	"failed":          i18n.WebFailed,
	"waiting":         i18n.WebWaiting,
	"unknownError":    i18n.WebUnknownError,
	"cancelAllAsk":    i18n.WebCancelAllAsk,
	"uploadCancelled": i18n.WebUploadCancelled,
}

// switchLang applies an explicit ?lang= choice and redirects back to the same
// page without it. Returns true when the response has already been written.
func (s *Server) switchLang(w http.ResponseWriter, r *http.Request) bool {
	tag := r.URL.Query().Get("lang")
	if tag == "" {
		return false
	}
	next, ok := i18n.Normalize(tag)
	if !ok {
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     langCookie,
		Value:    string(next),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
	})
	target := r.URL.EscapedPath()
	if r.URL.Query().Get("all") != "" {
		target += "?all=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
	return true
}

// langFor resolves the language for one request: an explicit choice first,
// then the host's configured language, then the browser's own preference.
func (s *Server) langFor(r *http.Request) i18n.Lang {
	if c, err := r.Cookie(langCookie); err == nil {
		if l, ok := i18n.Normalize(c.Value); ok {
			return l
		}
	}
	if s.langSet.Load() {
		return s.L()
	}
	if l, ok := acceptLang(r); ok {
		return l
	}
	return s.L()
}

func acceptLang(r *http.Request) (i18n.Lang, bool) {
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if l, ok := i18n.Normalize(tag); ok {
			return l, true
		}
	}
	return "", false
}

// shareHref builds the canonical URL of a directory inside a share. Every
// segment is escaped on its own so a name containing a slash or a space still
// addresses one entry.
func shareHref(id, dir string) string {
	var b strings.Builder
	b.WriteString("/s/")
	b.WriteString(url.PathEscape(id))
	b.WriteString("/")
	if dir != "" {
		for _, seg := range strings.Split(dir, "/") {
			b.WriteString(url.PathEscape(seg))
			b.WriteString("/")
		}
	}
	return b.String()
}

// langHref points at this same page in the other language.
func langHref(r *http.Request, cur i18n.Lang) string {
	next := i18n.EN
	if cur == i18n.EN {
		next = i18n.ZHCN
	}
	href := r.URL.EscapedPath() + "?lang=" + url.QueryEscape(string(next))
	if r.URL.Query().Get("all") != "" {
		href += "&all=1"
	}
	return href
}

func newPage(lang i18n.Lang, r *http.Request) *pageData {
	aria := i18n.T(lang, i18n.WebSwitchToEN)
	if lang == i18n.EN {
		aria = i18n.T(lang, i18n.WebSwitchToZH)
	}
	return &pageData{
		LangTag:      string(lang),
		Title:        "LanDrop",
		Build:        pageBuild,
		LangHref:     langHref(r, lang),
		LangLabel:    i18n.T(lang, i18n.WebLangButton),
		LangAria:     aria,
		PathLabel:    i18n.T(lang, i18n.WebPath),
		HomeHref:     "/",
		BackLabel:    i18n.T(lang, i18n.WebBack),
		BackAria:     i18n.T(lang, i18n.WebBackAria),
		Upload:       i18n.T(lang, i18n.WebUpload),
		UploadFiles:  i18n.T(lang, i18n.WebUploadFiles),
		UploadFolder: i18n.T(lang, i18n.WebUploadFolder),
		NewFolder:    i18n.T(lang, i18n.WebNewFolder),
		CancelAll:    i18n.T(lang, i18n.WebCancelAll),
		Hide:         i18n.T(lang, i18n.WebHide),
		SelectAll:    i18n.T(lang, i18n.WebSelectAll),
		DownloadZip:  i18n.T(lang, i18n.WebDownloadZip),
		ClearPick:    i18n.T(lang, i18n.WebClearPick),
		Download:     i18n.T(lang, i18n.WebDownload),
		Delete:       i18n.T(lang, i18n.WebDelete),
		SharedFolder: i18n.T(lang, i18n.WebSharedFolder),
		DropHere:     i18n.T(lang, i18n.WebDropHere),
		GoHome:       i18n.T(lang, i18n.WebGoHome),
		Runtime:      runtimeJSON(lang),
	}
}

func runtimeJSON(lang i18n.Lang) template.JS {
	m := make(map[string]string, len(runtimeMsgs))
	for k, msg := range runtimeMsgs {
		// No args: the placeholders have to survive so the browser can fill
		// them in. Same %s/%d convention as the Go side.
		m[k] = i18n.T(lang, msg)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return template.JS("{}")
	}
	return template.JS(b)
}

// chipText describes the server state next to the brand: how many folders are
// shared, and whether writes are allowed.
func chipText(lang i18n.Lang, shares int, readOnly bool) string {
	var label string
	if shares > 0 {
		label = i18n.T(lang, i18n.WebShareCount, shares)
	} else {
		label = i18n.T(lang, i18n.WebNotConfigured)
	}
	if readOnly {
		label += " · " + i18n.T(lang, i18n.WebReadOnly)
	}
	return label
}

func delayFor(i int) string {
	if i > 9 {
		i = 9
	}
	return strconv.FormatFloat(float64(i)*0.022, 'f', 3, 64)
}

// iconFor picks the glyph by extension. Deliberately a short list: a wrong
// guess is cheaper than a long table, and the name is right beside it.
func iconFor(name string) string {
	if name == "" {
		return "file"
	}
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic", ".heif", ".bmp", ".svg", ".avif", ".tiff":
		return "image"
	case ".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".flv", ".wmv":
		return "video"
	case ".mp3", ".wav", ".flac", ".aac", ".m4a", ".ogg", ".opus", ".wma":
		return "audio"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".iso":
		return "zip"
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt", ".md", ".csv", ".json":
		return "doc"
	}
	return "file"
}

// fmtWhen renders a timestamp the way a file manager does: the clock for
// today, the date for this year, the year before that.
func fmtWhen(lang i18n.Lang, t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	if t.Year() == now.Year() {
		if lang == i18n.ZHCN {
			return fmt.Sprintf("%d月%d日", int(t.Month()), t.Day())
		}
		return t.Format("Jan 2")
	}
	if lang == i18n.ZHCN {
		return t.Format("2006/1/2")
	}
	return t.Format("2006-01-02")
}

func entrySub(lang i18n.Lang, e Entry) string {
	if e.IsDir {
		return i18n.T(lang, i18n.WebFolder)
	}
	sub := humanBytes(e.Size)
	if e.ModTime > 0 {
		sub += " · " + fmtWhen(lang, time.UnixMilli(e.ModTime))
	}
	return sub
}

// render writes a complete page. The template is executed into a buffer first:
// a failure halfway through would otherwise leave a truncated document with a
// 200 already on the wire.
func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, p *pageData) {
	var buf bytes.Buffer
	if err := pageTmpl.ExecuteTemplate(&buf, "layout", p); err != nil {
		http.Error(w, "cannot render page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, lang i18n.Lang, code int, msg string) {
	p := newPage(lang, r)
	p.Mode = "error"
	p.Title = i18n.T(lang, i18n.WebErrorTitle) + " · LanDrop"
	p.ErrorTitle = i18n.T(lang, i18n.WebErrorTitle)
	p.ErrorMessage = msg
	p.HasBack = false
	s.render(w, r, code, p)
}

// handleRoot renders the share picker. A single share skips it: with one
// folder there is nothing to pick, and the extra tap is pure friction.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusNotFound, i18n.T(s.langFor(r), i18n.ErrUnknownEndpoint))
		return
	}
	if s.switchLang(w, r) {
		return
	}
	// This handler is the catch-all for GET, so anything that is not the
	// picker itself (a favicon request, a typo) has to say so instead of
	// handing back a page that looks like it worked.
	if r.URL.Path != "/" {
		s.renderError(w, r, s.langFor(r), http.StatusNotFound, i18n.T(s.langFor(r), i18n.ErrNotFound))
		return
	}

	lang := s.langFor(r)
	shares := s.shareList()
	if len(shares) == 1 {
		http.Redirect(w, r, shareHref(shares[0].ID, ""), http.StatusFound)
		return
	}

	p := newPage(lang, r)
	p.Mode = "roots"
	p.Title = "LanDrop"
	p.RootChip = chipText(lang, len(shares), s.readOnly.Load())
	p.Crumbs = []crumb{{Name: i18n.T(lang, i18n.WebHome), Current: true}}
	if len(shares) == 0 {
		p.Empty = true
		p.EmptyText = i18n.T(lang, i18n.WebNoShares)
	}
	for i, sh := range shares {
		p.Roots = append(p.Roots, viewRoot{
			ID:    sh.ID,
			Name:  sh.Name,
			Href:  shareHref(sh.ID, ""),
			Delay: delayFor(i),
		})
	}
	s.render(w, r, http.StatusOK, p)
}

// handleShare renders one directory listing. Every link on the page is a real
// URL, so refresh, bookmarking and the back button all work without a script.
func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	if s.switchLang(w, r) {
		return
	}

	lang := s.langFor(r)
	shareID := r.PathValue("share")
	dir := strings.Trim(r.PathValue("dir"), "/")

	entries, sh, status, err := s.listDir(lang, shareID, dir)
	if err != nil {
		s.renderError(w, r, lang, status, err.Error())
		return
	}

	shares := s.shareList()
	p := newPage(lang, r)
	p.Mode = "files"
	p.ShareID = sh.ID
	p.Dir = dir
	p.ReadOnly = s.readOnly.Load()
	p.CanWrite = !p.ReadOnly
	p.ShowDock = p.CanWrite
	p.RootChip = chipText(lang, len(shares), p.ReadOnly)

	if dir == "" {
		p.Title = sh.Name + " · LanDrop"
	} else {
		p.Title = path.Base(dir) + " · " + sh.Name
	}

	// Home only exists when there is something to go home to.
	if len(shares) > 1 {
		p.Crumbs = append(p.Crumbs, crumb{Name: i18n.T(lang, i18n.WebHome), Href: "/"})
	}
	p.Crumbs = append(p.Crumbs, crumb{Name: sh.Name, Href: shareHref(sh.ID, ""), Current: dir == ""})
	if dir != "" {
		parts := strings.Split(dir, "/")
		for i := range parts {
			p.Crumbs = append(p.Crumbs, crumb{
				Name:    parts[i],
				Href:    shareHref(sh.ID, strings.Join(parts[:i+1], "/")),
				Current: i == len(parts)-1,
			})
		}
	}
	for i := range p.Crumbs {
		p.Crumbs[i].Sep = i > 0
	}

	switch {
	case dir != "":
		parent := path.Dir(dir)
		if parent == "." {
			parent = ""
		}
		p.HasBack = true
		p.BackHref = shareHref(sh.ID, parent)
	case len(shares) > 1:
		p.HasBack = true
		p.BackHref = "/"
	}

	// Always set, not only when the folder is empty: the page keeps a hidden
	// empty state around so deleting the last row can reveal it without a
	// round trip.
	p.EmptyText = i18n.T(lang, i18n.WebEmptyFolder)
	p.Empty = len(entries) == 0

	shown := entries
	if r.URL.Query().Get("all") == "" && len(shown) > pageEntryLimit {
		shown = shown[:pageEntryLimit]
		p.Truncated = true
		p.TruncNote = i18n.T(lang, i18n.WebTruncated, pageEntryLimit, len(entries))
		p.ShowAll = i18n.T(lang, i18n.WebShowAll)
		p.ShowAllHref = shareHref(sh.ID, dir) + "?all=1"
	}

	for i, e := range shown {
		rel := e.Name
		if dir != "" {
			rel = dir + "/" + e.Name
		}
		ve := viewEntry{
			Name:       e.Name,
			Rel:        rel,
			IsDir:      e.IsDir,
			Sub:        entrySub(lang, e),
			Delay:      delayFor(i),
			AriaSelect: i18n.T(lang, i18n.WebSelectItem, e.Name),
		}
		if e.IsDir {
			ve.Icon = "dir"
			ve.Href = shareHref(sh.ID, rel)
		} else {
			ve.Icon = iconFor(e.Name)
			ve.DownloadHref = "/api/download?share=" + url.QueryEscape(sh.ID) +
				"&path=" + url.QueryEscape(rel)
			if s.thumbsOn.Load() && previewable(e.Name, s.ffmpegPath()) {
				ve.ThumbHref = "/api/thumb?share=" + url.QueryEscape(sh.ID) +
					"&path=" + url.QueryEscape(rel) +
					"&v=" + strconv.FormatInt(e.Size, 10) + "-" + strconv.FormatInt(e.ModTime, 10)
			}
		}
		p.Entries = append(p.Entries, ve)
	}

	s.render(w, r, http.StatusOK, p)
}

// serveStatic hands out the CSS and JS under a URL that changes with the
// build, so they can be cached forever and still be replaced on an upgrade.
func serveStatic(w http.ResponseWriter, r *http.Request) {
	// No directory listing: the embedded files are an implementation detail.
	if r.URL.Path == "/static/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.StripPrefix("/static", http.FileServerFS(pageAssets)).ServeHTTP(w, r)
}

// listDir reads one directory with the current filters applied, sorted for
// display. The status code and message come back ready to hand to a client.
func (s *Server) listDir(lang i18n.Lang, shareID, dir string) ([]Entry, Share, int, error) {
	abs, sh, err := s.resolve(lang, shareID, dir)
	if err != nil {
		return nil, Share{}, http.StatusForbidden, err
	}

	st, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, Share{}, http.StatusNotFound, errors.New(i18n.T(lang, i18n.ErrDirNotFound))
		}
		return nil, Share{}, http.StatusInternalServerError, err
	}
	if !st.IsDir() {
		return nil, Share{}, http.StatusBadRequest, errors.New(i18n.T(lang, i18n.ErrNotFolder))
	}

	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, Share{}, http.StatusInternalServerError, err
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
		// Upload bookkeeping is not user data. Showing a half-written .part
		// next to real files is how someone ends up opening a corrupt ISO.
		if isPartArtifact(de.Name()) {
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
			if _, _, err := s.resolve(lang, shareID, path.Join(dir, de.Name())); err != nil {
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

	return entries, sh, http.StatusOK, nil
}

// shareList copies the share slice for rendering.
func (s *Server) shareList() []Share {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Share, len(s.shares))
	copy(out, s.shares)
	return out
}
