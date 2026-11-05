package transfer

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CyanHwang/lan-drop/internal/i18n"
)

// The browser UI is embedded in the binary and served over the LAN listener,
// not through the Wails asset handler (which only feeds the WebView).
//
//go:embed mobile.html
var mobileHTML []byte

// Share is one directory exposed over the LAN.
type Share struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// ShareID derives a stable short id from an absolute path. Lowercased so that
// Windows paths differing only by case collapse to the same share.
func ShareID(path string) string {
	norm := strings.ToLower(filepath.Clean(path))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:4])
}

// Info is the snapshot the desktop UI renders.
type Info struct {
	Running  bool     `json:"running"`
	Port     int      `json:"port"`
	URLs     []string `json:"urls"`
	Since    int64    `json:"since"`
	ReadOnly bool     `json:"readOnly"`
	Shares   []Share  `json:"shares"`
}

// EmptyInfo is the not-running snapshot. Slices are explicitly non-nil: a nil
// slice marshals to JSON null, and the frontend reads urls.length, which would
// throw and blank the whole window.
func EmptyInfo() Info {
	return Info{URLs: []string{}, Shares: []Share{}}
}

// Progress is pushed to the desktop UI as a Wails event.
type Progress struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Dir       string `json:"dir"`
	Share     string `json:"share"`
	Direction string `json:"direction"`
	Loaded    int64  `json:"loaded"`
	Total     int64  `json:"total"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
	Speed     int64  `json:"speed"`
	ElapsedMS int64  `json:"elapsedMs"`
	// At is when the transfer started, in unix milliseconds. The UI shows it as
	// a clock time next to the file name.
	At int64 `json:"at"`
	// ClientIP and Client describe the peer that initiated the transfer. They
	// are informational only, derived from the socket address and User-Agent.
	ClientIP string `json:"clientIp"`
	Client   string `json:"client"`
}

// Entry is one item in a directory listing.
type Entry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"modTime"`
}

// StartOptions tunes how the listener comes up.
type StartOptions struct {
	// Port is the first port tried. Zero falls back to DefaultPort.
	Port int
	// Host is the interface to bind. Empty means all interfaces.
	Host string
	// StrictPort fails instead of walking forward when Port is taken. Needed
	// by container and reverse-proxy deployments where the port is fixed.
	StrictPort bool
	// ReadOnly rejects every write endpoint, so the host can share files
	// without letting the network modify them.
	ReadOnly bool
	// Lang selects the language for error messages. Zero value means English.
	Lang i18n.Lang
	// ShowHidden includes entries carrying the OS hidden attribute (Windows).
	// On Unix there is no such attribute, so dotfiles are the only convention
	// and this flag has no effect.
	ShowHidden bool
	// ShowDotfiles includes entries whose name starts with a dot.
	ShowDotfiles bool
	// Filter is the user's exclusion list, e.g. "*.tmp, node_modules". See
	// ParseFilter for the accepted syntax.
	Filter string
}

// Server owns the LAN-facing HTTP listener. It is deliberately separate from
// the Wails asset server: that one only feeds the WebView and never binds a
// TCP port, so other devices cannot reach it.
type Server struct {
	mu       sync.RWMutex
	shares   []Share
	port     int
	ln       net.Listener
	httpSrv  *http.Server
	since    time.Time
	readOnly atomic.Bool
	lang     atomic.Value // i18n.Lang
	// Listing filters. Atomic so the desktop can flip them while the server
	// is running without a restart; the next request picks them up.
	showHidden   atomic.Bool
	showDotfiles atomic.Bool
	filter       atomic.Value // filterSet

	ips ipCache

	emit func(name string, data any)
}

// ipCache memoises the LAN address list. Enumerating adapters costs ~15ms on
// Windows, and /api/info is hit on every page load and every desktop status
// refresh, so recomputing it per request is pure waste. The TTL keeps a
// network switch (Wi-Fi to Ethernet, VPN up) reflected within seconds.
type ipCache struct {
	mu  sync.Mutex
	ips []string
	at  time.Time
}

const ipCacheTTL = 5 * time.Second

func (c *ipCache) get() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ips != nil && time.Since(c.at) < ipCacheTTL {
		return c.ips
	}
	c.ips = LANIPs()
	c.at = time.Now()
	return c.ips
}

// New builds a server. emit may be nil; it is only used to notify the desktop UI.
func New(emit func(string, any)) *Server {
	s := &Server{emit: emit}
	s.lang.Store(i18n.EN)
	return s
}

// L returns the language used for API error messages.
func (s *Server) L() i18n.Lang {
	if v, ok := s.lang.Load().(i18n.Lang); ok && v != "" {
		return v
	}
	return i18n.EN
}

// SetViewOptions updates the listing filters. Safe to call while serving.
func (s *Server) SetViewOptions(showHidden, showDotfiles bool) {
	s.showHidden.Store(showHidden)
	s.showDotfiles.Store(showDotfiles)
}

// SetFilter replaces the exclusion list. Invalid entries are dropped, so this
// never fails — the field is free text typed by a human.
func (s *Server) SetFilter(raw string) {
	s.filter.Store(ParseFilter(raw))
}

// Filter returns the compiled exclusion list.
func (s *Server) Filter() filterSet {
	if v, ok := s.filter.Load().(filterSet); ok {
		return v
	}
	return filterSet{}
}

// ViewOptions reports the current listing filters.
func (s *Server) ViewOptions() (showHidden, showDotfiles bool) {
	return s.showHidden.Load(), s.showDotfiles.Load()
}

// SetLanguage switches the language of subsequent API error messages.
func (s *Server) SetLanguage(l i18n.Lang) {
	if l == "" {
		l = i18n.EN
	}
	s.lang.Store(l)
}

func (s *Server) publish(name string, data any) {
	if s.emit != nil {
		s.emit(name, data)
	}
}

// Start validates every share, binds the listener and begins serving.
func (s *Server) Start(shares []Share, preferredPort int) (Info, error) {
	return s.StartWithOptions(shares, StartOptions{Port: preferredPort})
}

// StartWithOptions is Start with explicit control over the bind address and
// write permissions.
func (s *Server) StartWithOptions(shares []Share, opt StartOptions) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if opt.Lang != "" {
		s.lang.Store(opt.Lang)
	}
	lang := s.L()

	if s.httpSrv != nil {
		return EmptyInfo(), errors.New(i18n.T(lang, i18n.ErrServerRunning))
	}
	if len(shares) == 0 {
		return EmptyInfo(), errors.New(i18n.T(lang, i18n.ErrNoShares))
	}

	validated := make([]Share, 0, len(shares))
	seen := map[string]bool{}
	for _, sh := range shares {
		abs, err := filepath.Abs(sh.Path)
		if err != nil {
			return EmptyInfo(), errors.New(i18n.T(lang, i18n.ErrSharePathInvalid, sh.Name))
		}
		st, err := os.Stat(abs)
		if err != nil {
			return EmptyInfo(), errors.New(i18n.T(lang, i18n.ErrShareInaccessible, sh.Name))
		}
		if !st.IsDir() {
			return EmptyInfo(), errors.New(i18n.T(lang, i18n.ErrShareNotFolder, sh.Name))
		}
		id := sh.ID
		if id == "" {
			id = ShareID(abs)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		validated = append(validated, Share{ID: id, Name: sh.Name, Path: abs})
	}
	if len(validated) == 0 {
		return EmptyInfo(), errors.New(i18n.T(lang, i18n.ErrNoUsableShares))
	}

	ln, port, err := listenFree(opt.Port, opt.Host, opt.StrictPort)
	if err != nil {
		return EmptyInfo(), errors.New(i18n.T(lang, i18n.ErrListen, err))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/list", s.handleList)
	mux.HandleFunc("GET /api/download", s.handleDownload)
	mux.HandleFunc("GET /api/probe", s.handleProbe)
	mux.HandleFunc("PUT /api/upload", s.handleUpload)
	mux.HandleFunc("POST /api/mkdir", s.handleMkdir)
	mux.HandleFunc("DELETE /api/file", s.handleDelete)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /", s.handleIndex)

	srv := &http.Server{
		Handler: mux,
		// Only the header read and idle keep-alives are bounded. Read/Write
		// timeouts must stay zero: a multi-gigabyte transfer is a single
		// long-lived request.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	s.shares = validated
	s.port = port
	s.ln = ln
	s.httpSrv = srv
	s.since = time.Now()
	s.readOnly.Store(opt.ReadOnly)
	s.showHidden.Store(opt.ShowHidden)
	s.showDotfiles.Store(opt.ShowDotfiles)
	s.filter.Store(ParseFilter(opt.Filter))

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.publish("server:error", err.Error())
		}
	}()

	return s.infoLocked(), nil
}

// Stop closes the listener. In-flight transfers are aborted.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.httpSrv == nil {
		return nil
	}
	err := s.httpSrv.Close()
	s.httpSrv = nil
	s.ln = nil
	s.port = 0
	s.shares = nil
	s.since = time.Time{}
	s.readOnly.Store(false)
	s.showHidden.Store(false)
	s.showDotfiles.Store(false)
	s.filter.Store(filterSet{})
	return err
}

func (s *Server) Info() Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.infoLocked()
}

func (s *Server) infoLocked() Info {
	if s.httpSrv == nil {
		return EmptyInfo()
	}
	ips := s.ips.get()
	urls := make([]string, 0, len(ips))
	for _, ip := range ips {
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip, s.port))
	}
	shares := make([]Share, len(s.shares))
	copy(shares, s.shares)
	return Info{
		Running:  true,
		Port:     s.port,
		URLs:     urls,
		Since:    s.since.UnixMilli(),
		ReadOnly: s.readOnly.Load(),
		Shares:   shares,
	}
}

// shareByID returns a copy of the share with the given id.
func (s *Server) shareByID(id string) (Share, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sh := range s.shares {
		if sh.ID == id {
			return sh, true
		}
	}
	return Share{}, false
}

var (
	errBadPath     = errors.New("invalid path")
	errInvalidName = errors.New("invalid file name")
)

// resolve maps a share id plus a URL-relative path onto the filesystem and
// refuses anything that escapes that share's root, including via symlinks.
// Errors are already localised: callers surface them straight to the client.
func (s *Server) resolve(shareID, rel string) (string, Share, error) {
	lang := s.L()
	sh, ok := s.shareByID(shareID)
	if !ok {
		return "", Share{}, errors.New(i18n.T(lang, i18n.ErrShareNotFound))
	}
	abs, err := resolveIn(sh.Path, rel)
	if err != nil {
		return "", Share{}, errors.New(i18n.T(lang, i18n.ErrBadPath))
	}
	return abs, sh, nil
}

func resolveIn(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimPrefix(rel, "/")

	// filepath.Join cleans the result, so "a/../../etc" collapses upward.
	joined := filepath.Join(root, filepath.FromSlash(rel))

	// Lexical check first: cheap, and it rejects the obvious "../" attempts
	// before touching the filesystem.
	if !within(root, joined) {
		return "", errBadPath
	}

	// Then the real check. EvalSymlinks alone is not enough: the target of an
	// upload does not exist yet, so it returns an error and a symlinked parent
	// directory would slip through. Resolve the deepest existing ancestor
	// instead and re-append the part that is about to be created.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errBadPath
	}
	real, err := evalDeepest(joined)
	if err != nil {
		return "", errBadPath
	}
	if !within(realRoot, real) {
		return "", errBadPath
	}

	return joined, nil
}

// evalDeepest resolves symlinks on the deepest existing ancestor of p and
// appends the remaining components verbatim. A path that does not exist at all
// is returned unchanged after its existing prefix has been resolved.
func evalDeepest(p string) (string, error) {
	rest := ""
	cur := filepath.Clean(p)
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if rest == "" {
				return real, nil
			}
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// Reached the volume root without finding anything that exists.
			return p, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether p is root or lives underneath it.
func within(root, p string) bool {
	check, err := filepath.Rel(root, p)
	if err != nil || filepath.IsAbs(check) {
		return false
	}
	return check != ".." && !strings.HasPrefix(check, ".."+string(filepath.Separator))
}

// sanitizeName strips directory components and characters Windows rejects.
func sanitizeName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", errInvalidName
	}
	if strings.ContainsAny(name, `<>:"/|?*`) {
		repl := strings.NewReplacer(
			"<", "_", ">", "_", ":", "_", `"`, "_",
			"/", "_", "|", "_", "?", "_", "*", "_",
		)
		name = repl.Replace(name)
	}
	// Control characters are invalid on every platform.
	cleaned := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 {
			continue
		}
		cleaned = append(cleaned, r)
	}
	if len(cleaned) == 0 {
		return "", errInvalidName
	}
	return string(cleaned), nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// throttle keeps high-frequency progress events from flooding the WebView.
type throttle struct {
	last int64
}

func (t *throttle) allow() bool {
	now := time.Now().UnixMilli()
	prev := atomic.LoadInt64(&t.last)
	if now-prev < 120 {
		return false
	}
	return atomic.CompareAndSwapInt64(&t.last, prev, now)
}

func (s *Server) newReporter(id, name, dir, share, direction string, total int64, clientIP, client string) *reporter {
	return &reporter{
		srv:       s,
		id:        id,
		name:      name,
		dir:       dir,
		share:     share,
		direction: direction,
		total:     total,
		start:     time.Now(),
		clientIP:  clientIP,
		client:    client,
	}
}

type reporter struct {
	srv       *Server
	id        string
	name      string
	dir       string
	share     string
	direction string
	total     int64
	start     time.Time
	clientIP  string
	client    string
	th        throttle
}

func (r *reporter) send(loaded int64, done bool, errMsg string) {
	elapsed := time.Since(r.start).Milliseconds()
	var speed int64
	if elapsed > 0 {
		speed = loaded * 1000 / elapsed
	}
	r.srv.publish("transfer:progress", Progress{
		ID:        r.id,
		Name:      r.name,
		Dir:       r.dir,
		Share:     r.share,
		Direction: r.direction,
		Loaded:    loaded,
		Total:     r.total,
		Done:      done,
		Error:     errMsg,
		Speed:     speed,
		ElapsedMS: elapsed,
		At:        r.start.UnixMilli(),
		ClientIP:  r.clientIP,
		Client:    r.client,
	})
}

func (r *reporter) tick(loaded int64) {
	if r.th.allow() {
		r.send(loaded, false, "")
	}
}

func (r *reporter) finish(loaded int64, errMsg string) {
	r.send(loaded, true, errMsg)
}

// copyStream moves src to dst in fixed-size chunks, reporting progress as it
// goes. No buffering beyond one chunk, so a 100 GB upload costs 256 KB of RAM.
func copyStream(dst io.Writer, src io.Reader, r *reporter) (int64, error) {
	buf := make([]byte, 256*1024)
	var written int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return written, writeErr
			}
			written += int64(n)
			if r != nil {
				r.tick(written)
			}
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}
