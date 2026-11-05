// Package config persists the user's settings in a portable config.ini file
// located next to the executable.
package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CyanHwang/lan-drop/internal/i18n"
	"github.com/CyanHwang/lan-drop/internal/transfer"
)

// Config represents the persisted application settings.
type Config struct {
	// Language is "en" or "zh-CN". Empty means it has never been resolved.
	Language string `json:"language"`
	// ShowHidden lists entries carrying the OS hidden attribute (Windows only).
	ShowHidden bool `json:"showHidden"`
	// ShowDotfiles lists entries whose name starts with a dot.
	ShowDotfiles bool             `json:"showDotfiles"`
	Shares       []transfer.Share `json:"shares"`
}

// FilePath returns the absolute path to config.ini alongside the executable.
// If running via `go run` (where the binary is placed in a temp folder), it
// falls back to the current working directory.
func FilePath() string {
	exe, err := os.Executable()
	if err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		lower := strings.ToLower(dir)
		if !strings.Contains(lower, "go-build") && !strings.Contains(lower, "temp") {
			return filepath.Join(dir, "config.ini")
		}
	}
	abs, err := filepath.Abs("config.ini")
	if err == nil {
		return abs
	}
	return "config.ini"
}

// legacyJSONPath returns the old %APPDATA%/LanDrop/config.json location.
func legacyJSONPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "LanDrop", "config.json")
}

func empty() Config {
	return Config{Shares: []transfer.Share{}}
}

// Load reads the settings from config.ini. If config.ini does not exist yet but
// the legacy AppData json file does, it automatically migrates the settings.
func Load() Config {
	p := FilePath()

	if _, err := os.Stat(p); os.IsNotExist(err) {
		if oldPath := legacyJSONPath(); oldPath != "" {
			if oldData, err := os.ReadFile(oldPath); err == nil {
				var legacy Config
				if err := json.Unmarshal(oldData, &legacy); err == nil && len(legacy.Shares) > 0 {
					_ = Save(legacy)
					_ = os.Remove(oldPath) // Clean up legacy file
					return legacy
				}
			}
		}
		return empty()
	}

	return loadFile(p)
}

// LoadFrom reads the settings from an explicit path. An empty path behaves
// like Load. Missing files yield empty settings rather than an error: a fresh
// install legitimately has no config yet.
func LoadFrom(path string) Config {
	if path == "" {
		return Load()
	}
	if _, err := os.Stat(path); err != nil {
		return empty()
	}
	return loadFile(path)
}

func loadFile(p string) Config {
	f, err := os.Open(p)
	if err != nil {
		return empty()
	}
	defer f.Close()

	cfg := empty()
	seen := make(map[string]bool)
	section := ""
	kv := make(map[string]string)

	// flush commits the section that just ended. A section is a shared folder
	// when it carries a "path" key; [general] carries application settings.
	flush := func() {
		if section != "" {
			if strings.HasPrefix(section, "general") {
				if v := kv["language"]; v != "" {
					cfg.Language = v
				}
				cfg.ShowHidden = parseBool(kv["show_hidden"])
				cfg.ShowDotfiles = parseBool(kv["show_dotfiles"])
			} else if raw := kv["path"]; raw != "" {
				path := filepath.Clean(raw)
				name := kv["name"]
				if name == "" {
					name = filepath.Base(path)
				}
				id := transfer.ShareID(path)
				if !seen[id] {
					seen[id] = true
					cfg.Shares = append(cfg.Shares, transfer.Share{ID: id, Name: name, Path: path})
				}
			}
		}
		kv = make(map[string]string)
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header: [general], [share_1], [share.2], ...
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || section == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		kv[key] = strings.TrimSpace(parts[1])
	}
	flush()

	if cfg.Shares == nil {
		cfg.Shares = []transfer.Share{}
	}
	return cfg
}

// Save writes the settings to config.ini in a clean, human-readable format.
// Hand-written comments are not preserved: the app rewrites the whole file.
func Save(c Config) error {
	return saveTo(FilePath(), c)
}

// saveTo is Save with an explicit destination, kept separate so tests can
// exercise the round trip without touching the real config file.
func saveTo(p string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	lang := c.Language
	if lang == "" {
		// Never leave the field blank: a config without a language would be
		// re-detected on every launch.
		lang = string(i18n.Detect())
	}

	var b strings.Builder
	b.WriteString("; LanDrop configuration file\n")
	b.WriteString("; Edit it with any text editor to add or change shared folders.\n\n")
	b.WriteString("[general]\r\n")
	fmt.Fprintf(&b, "language = %s\r\n", lang)
	fmt.Fprintf(&b, "show_hidden = %t\r\n", c.ShowHidden)
	fmt.Fprintf(&b, "show_dotfiles = %t\r\n\r\n", c.ShowDotfiles)

	for i, sh := range c.Shares {
		fmt.Fprintf(&b, "[share_%d]\r\n", i+1)
		fmt.Fprintf(&b, "name = %s\r\n", sh.Name)
		fmt.Fprintf(&b, "path = %s\r\n\r\n", sh.Path)
	}

	return os.WriteFile(p, []byte(b.String()), 0o644)
}

// parseBool accepts the spellings people actually type in an ini file.
func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// SetLanguage rewrites only the language field, keeping the share list intact.
func SetLanguage(lang string) error {
	cfg := Load()
	cfg.Language = lang
	return Save(cfg)
}

// EnsureLanguage returns the configured language, detecting the OS language
// and persisting it the first time. Once written, the stored value always
// wins — a later OS language change must not flip the UI behind the user's
// back, because they may have picked it explicitly.
func EnsureLanguage() string {
	cfg := Load()
	if l, ok := i18n.Normalize(cfg.Language); ok {
		return string(l)
	}
	detected := string(i18n.Detect())
	cfg.Language = detected
	_ = Save(cfg)
	return detected
}
