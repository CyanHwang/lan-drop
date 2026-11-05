package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CyanHwang/lan-drop/internal/i18n"
	"github.com/CyanHwang/lan-drop/internal/transfer"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "lan-drop.ini")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadParsesGeneralAndShares(t *testing.T) {
	p := writeTemp(t, `; hand-written comment
[general]
language = zh-CN

[share_1]
name = Photos
path = /data/photos

[share_2]
path = /data/docs
`)

	cfg := loadFile(p)

	if cfg.Language != "zh-CN" {
		t.Errorf("language = %q, want zh-CN", cfg.Language)
	}
	if len(cfg.Shares) != 2 {
		t.Fatalf("shares = %d, want 2", len(cfg.Shares))
	}
	if cfg.Shares[0].Name != "Photos" {
		t.Errorf("share 0 name = %q", cfg.Shares[0].Name)
	}
	// A share without a name falls back to the folder's base name.
	if cfg.Shares[1].Name != "docs" {
		t.Errorf("share 1 name = %q, want docs", cfg.Shares[1].Name)
	}
}

func TestLoadWithoutGeneralSection(t *testing.T) {
	// Old configs had no [general] block; they must still load.
	p := writeTemp(t, `[share_1]
name = Docs
path = /data/docs
`)

	cfg := loadFile(p)
	if cfg.Language != "" {
		t.Errorf("language = %q, want empty", cfg.Language)
	}
	if len(cfg.Shares) != 1 {
		t.Fatalf("shares = %d, want 1", len(cfg.Shares))
	}
}

func TestLoadDeduplicatesByPath(t *testing.T) {
	p := writeTemp(t, `[share_1]
name = A
path = /data/docs

[share_2]
name = B
path = /data/docs/
`)

	cfg := loadFile(p)
	if len(cfg.Shares) != 1 {
		t.Fatalf("shares = %d, want 1 after dedupe", len(cfg.Shares))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lan-drop.ini")

	in := Config{
		Language: "en",
		Shares: []transfer.Share{
			{Name: "Photos", Path: filepath.FromSlash("/data/photos")},
			{Name: "文档", Path: filepath.FromSlash("/data/docs")},
		},
	}
	if err := saveTo(p, in); err != nil {
		t.Fatalf("save: %v", err)
	}

	out := loadFile(p)
	if out.Language != "en" {
		t.Errorf("language = %q, want en", out.Language)
	}
	if len(out.Shares) != 2 {
		t.Fatalf("shares = %d, want 2", len(out.Shares))
	}
	if out.Shares[0].Path != filepath.Clean(in.Shares[0].Path) {
		t.Errorf("path = %q, want %q", out.Shares[0].Path, filepath.Clean(in.Shares[0].Path))
	}
	if out.Shares[1].Name != "文档" {
		t.Errorf("non-ASCII name lost: %q", out.Shares[1].Name)
	}
	// IDs are derived, never stored; they must be stable across a round trip.
	if out.Shares[0].ID != transfer.ShareID(in.Shares[0].Path) {
		t.Errorf("id = %q, want derived id", out.Shares[0].ID)
	}
}

// A config without a language must be written with a concrete one, otherwise
// every launch would re-detect the OS language and ignore a later change.
func TestSaveFillsLanguageWhenEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lan-drop.ini")
	if err := saveTo(p, Config{Shares: []transfer.Share{}}); err != nil {
		t.Fatal(err)
	}

	cfg := loadFile(p)
	if _, ok := i18n.Normalize(cfg.Language); !ok {
		t.Fatalf("saved language %q is not a supported tag", cfg.Language)
	}
	if strings.Contains(cfg.Language, "auto") {
		t.Fatalf("saved language = %q, want a concrete tag", cfg.Language)
	}
}

// Once a language is stored, loading must not overwrite it with the OS one.
func TestStoredLanguageWins(t *testing.T) {
	p := writeTemp(t, `[general]
language = en

[share_1]
name = Docs
path = /data/docs
`)

	cfg := loadFile(p)
	if cfg.Language != "en" {
		t.Fatalf("language = %q, want en", cfg.Language)
	}
	if got := i18n.Resolve(cfg.Language); got != i18n.EN {
		t.Fatalf("Resolve(%q) = %q, want en", cfg.Language, got)
	}
}

// First run must detect the OS language and persist it; later runs must honour
// whatever is stored, even if the OS language is different.
func TestEnsureLanguageDetectsAndPersists(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	got := EnsureLanguage()
	if _, ok := i18n.Normalize(got); !ok {
		t.Fatalf("EnsureLanguage() = %q, want a supported tag", got)
	}

	cfg := Load()
	if cfg.Language != got {
		t.Fatalf("persisted language = %q, want %q", cfg.Language, got)
	}

	if err := SetLanguage("en"); err != nil {
		t.Fatal(err)
	}
	if again := EnsureLanguage(); again != "en" {
		t.Fatalf("EnsureLanguage() = %q after SetLanguage(en), want en", again)
	}
}

func TestViewOptionsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lan-drop.ini")

	if err := saveTo(p, Config{Language: "en", ShowHidden: true, ShowDotfiles: false}); err != nil {
		t.Fatal(err)
	}
	cfg := loadFile(p)
	if !cfg.ShowHidden || cfg.ShowDotfiles {
		t.Fatalf("got showHidden=%v showDotfiles=%v, want true/false", cfg.ShowHidden, cfg.ShowDotfiles)
	}

	// Both on, both off: the pair must survive independently.
	if err := saveTo(p, Config{Language: "en", ShowHidden: true, ShowDotfiles: true}); err != nil {
		t.Fatal(err)
	}
	if cfg := loadFile(p); !cfg.ShowHidden || !cfg.ShowDotfiles {
		t.Fatalf("got %+v, want both true", cfg)
	}
}

// People edit this file by hand, so accept the usual boolean spellings.
func TestParseBoolSpellings(t *testing.T) {
	for _, v := range []string{"true", "TRUE", "1", "yes", "on", " on "} {
		if !parseBool(v) {
			t.Errorf("parseBool(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"false", "0", "no", "off", "", "nope"} {
		if parseBool(v) {
			t.Errorf("parseBool(%q) = true, want false", v)
		}
	}
}

// A pre-rename config.ini is read once and copied to lan-drop.ini. The old
// file is deliberately left in place, and later loads must use the new one.
func TestLegacyINIMigration(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	body := "[general]\nlanguage = en\nshow_dotfiles = true\n\n[share_1]\nname = Docs\npath = /data/docs\n"
	if err := os.WriteFile("config.ini", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if len(cfg.Shares) != 1 || cfg.Language != "en" || !cfg.ShowDotfiles {
		t.Fatalf("migrated config = %+v", cfg)
	}
	if _, err := os.Stat("lan-drop.ini"); err != nil {
		t.Fatalf("new config not written: %v", err)
	}
	if _, err := os.Stat("config.ini"); err != nil {
		t.Fatalf("old config should be preserved: %v", err)
	}

	if err := os.WriteFile("config.ini", []byte("[general]\nlanguage = zh-CN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := Load(); again.Language != "en" {
		t.Fatalf("second load = %q, want en from the new file", again.Language)
	}
}
