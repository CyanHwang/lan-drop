//go:build !server

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/CyanHwang/lan-drop/internal/config"
	"github.com/CyanHwang/lan-drop/internal/i18n"
	"github.com/CyanHwang/lan-drop/internal/transfer"
)

// AppService is the bridge between the React desktop UI and the LAN server.
// It owns the transfer.Server lifetime, the persisted share list, and re-emits
// transfer progress as Wails events.
type AppService struct {
	mu   sync.Mutex
	app  *application.App
	srv  *transfer.Server
	lang i18n.Lang
	// onLangChange lets the shell rebuild the tray menu after a switch.
	onLangChange func(i18n.Lang)
}

func NewAppService() *AppService {
	// EnsureLanguage detects the OS language and writes it to config.ini the
	// first time, so the UI has a concrete language from the very first frame.
	return &AppService{lang: i18n.Resolve(config.EnsureLanguage())}
}

func (a *AppService) attach(app *application.App) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.app = app
	a.srv = transfer.New(func(name string, data any) {
		app.Event.Emit(name, data)
	})
	a.srv.SetLanguage(a.lang)
}

// setLangChangeHook registers a callback fired whenever the language changes.
func (a *AppService) setLangChangeHook(fn func(i18n.Lang)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onLangChange = fn
}

// L returns the active UI language.
func (a *AppService) L() i18n.Lang {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lang == "" {
		return i18n.EN
	}
	return a.lang
}

// err builds a localised error for the UI.
func (a *AppService) err(m i18n.Msg, args ...any) error {
	return errors.New(i18n.T(a.L(), m, args...))
}

// Language returns the active UI language tag ("en" or "zh-CN").
func (a *AppService) Language() string {
	return string(a.L())
}

// SetLanguage persists the choice to config.ini and applies it immediately.
// Returns the language in effect so the UI can re-render without a second call.
func (a *AppService) SetLanguage(tag string) (string, error) {
	next, ok := i18n.Normalize(strings.TrimSpace(tag))
	if !ok {
		return "", fmt.Errorf("unsupported language: %s", tag)
	}
	if err := config.SetLanguage(string(next)); err != nil {
		return "", a.err(i18n.ErrConfigWrite, err)
	}

	a.mu.Lock()
	a.lang = next
	srv := a.srv
	hook := a.onLangChange
	a.mu.Unlock()

	if srv != nil {
		srv.SetLanguage(next)
	}
	if hook != nil {
		hook(next)
	}
	return string(next), nil
}

// ServiceShutdown stops the listener when the app quits.
func (a *AppService) ServiceShutdown() error {
	a.mu.Lock()
	srv := a.srv
	a.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Stop()
}

// Shares returns the persisted share list.
func (a *AppService) Shares() []transfer.Share {
	return config.Load().Shares
}

// AddShare opens the native directory picker and appends the choice. Returns
// the full list so the UI can re-render from a single source of truth.
func (a *AppService) AddShare() ([]transfer.Share, error) {
	a.mu.Lock()
	app := a.app
	a.mu.Unlock()
	if app == nil {
		return nil, a.err(i18n.ErrAppNotReady)
	}

	dir, err := app.Dialog.OpenFile().
		SetTitle(i18n.T(a.L(), i18n.DialogPickFolder)).
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true).
		PromptForSingleSelection()
	if err != nil {
		// Closing the picker is a normal outcome, not a failure worth surfacing.
		if isDialogCancelled(err) {
			return config.Load().Shares, nil
		}
		return nil, err
	}

	cfg := config.Load()
	if dir == "" {
		// Some platforms return an empty path instead of an error on cancel.
		return cfg.Shares, nil
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, a.err(i18n.ErrInvalidPath, err)
	}
	id := transfer.ShareID(abs)
	for _, sh := range cfg.Shares {
		if sh.ID == id {
			return cfg.Shares, nil
		}
	}

	cfg.Shares = append(cfg.Shares, transfer.Share{
		ID:   id,
		Name: uniqueName(cfg.Shares, filepath.Base(abs)),
		Path: abs,
	})
	if err := config.Save(cfg); err != nil {
		return nil, a.err(i18n.ErrConfigWrite, err)
	}
	return cfg.Shares, nil
}

func (a *AppService) RemoveShare(id string) ([]transfer.Share, error) {
	cfg := config.Load()
	kept := make([]transfer.Share, 0, len(cfg.Shares))
	for _, sh := range cfg.Shares {
		if sh.ID != id {
			kept = append(kept, sh)
		}
	}
	cfg.Shares = kept
	if err := config.Save(cfg); err != nil {
		return nil, a.err(i18n.ErrConfigWrite, err)
	}
	return cfg.Shares, nil
}

// Start serves every persisted share on the LAN.
func (a *AppService) Start(port int) (transfer.Info, error) {
	a.mu.Lock()
	srv := a.srv
	a.mu.Unlock()
	if srv == nil {
		return transfer.EmptyInfo(), a.err(i18n.ErrAppNotReady)
	}
	cfg := config.Load()
	return srv.StartWithOptions(cfg.Shares, transfer.StartOptions{
		Port: port,
		Lang: i18n.Resolve(cfg.Language),
	})
}

func (a *AppService) Stop() (transfer.Info, error) {
	a.mu.Lock()
	srv := a.srv
	a.mu.Unlock()
	if srv == nil {
		return transfer.EmptyInfo(), a.err(i18n.ErrAppNotReady)
	}
	if err := srv.Stop(); err != nil {
		return transfer.EmptyInfo(), err
	}
	return srv.Info(), nil
}

func (a *AppService) Status() transfer.Info {
	a.mu.Lock()
	srv := a.srv
	a.mu.Unlock()
	if srv == nil {
		return transfer.EmptyInfo()
	}
	return srv.Info()
}

// CopyText puts arbitrary text on the system clipboard. The WebView clipboard
// API is unreliable without a secure origin, so the UI goes through Go.
func (a *AppService) CopyText(text string) error {
	a.mu.Lock()
	app := a.app
	a.mu.Unlock()
	if app == nil {
		return a.err(i18n.ErrAppNotReady)
	}
	if !app.Clipboard.SetText(text) {
		return a.err(i18n.ErrClipboard)
	}
	return nil
}

// OpenInBrowser opens the share URL in the system browser.
func (a *AppService) OpenInBrowser() error {
	a.mu.Lock()
	app, srv := a.app, a.srv
	a.mu.Unlock()
	if app == nil || srv == nil {
		return a.err(i18n.ErrAppNotReady)
	}
	info := srv.Info()
	if !info.Running || len(info.URLs) == 0 {
		return a.err(i18n.ErrServiceNotRunning)
	}
	return app.Browser.OpenURL(info.URLs[0])
}

// RevealShare opens one shared folder in the OS file manager.
func (a *AppService) RevealShare(id string) error {
	a.mu.Lock()
	app, srv := a.app, a.srv
	a.mu.Unlock()
	if app == nil || srv == nil {
		return a.err(i18n.ErrAppNotReady)
	}
	for _, sh := range srv.Info().Shares {
		if sh.ID == id {
			return app.Browser.OpenFile(sh.Path)
		}
	}
	return a.err(i18n.ErrShareNotFound)
}

// isDialogCancelled reports whether err is the "user closed the picker" signal.
// Wails surfaces it as a plain error with no typed sentinel, so the message is
// the only thing to match on.
func isDialogCancelled(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "cancel") || strings.Contains(msg, "取消")
}

// uniqueName avoids duplicate labels in the share list.
func uniqueName(existing []transfer.Share, base string) string {
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "Shared folder"
	}
	used := make(map[string]bool, len(existing))
	for _, sh := range existing {
		used[sh.Name] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)", base, i)
		if !used[candidate] {
			return candidate
		}
	}
}
