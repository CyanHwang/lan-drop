// Command syncversion keeps every version string in the repo in step with the
// single source of truth: build/config.yml -> info.version.
//
// Usage:
//
//	go run ./scripts/syncversion          # check that all files agree
//	go run ./scripts/syncversion -set 1.2.3
//	go run ./scripts/syncversion -set 1.2.3 -write
//
// Without -write the command only reports mismatches and exits non-zero, which
// makes it usable as a CI guard. With -set it prints the edits it would make;
// add -write to apply them.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var versionLine = regexp.MustCompile(`^(\s*version:\s*)"([^"]+)"(\s*(?:#.*)?)$`)

type edit struct {
	file string
	// want is how many version-bearing lines in this file should carry the
	// target version. Checking this count catches both stale and partial edits.
	want int
	// four tells the scanner to expect MAJOR.MINOR.PATCH.0 (MSIX packages).
	four bool
	// plist restricts scanning to <string> values that follow a CFBundle
	// version key, so unrelated values such as LSMinimumSystemVersion are
	// left alone.
	plist bool
	// limit caps how many leading version lines belong to this file. Needed
	// for package-lock.json, whose dependency entries also carry "version".
	limit int
}

func main() {
	set := flag.String("set", "", "new semantic version (e.g. 1.2.3); empty means check only")
	write := flag.Bool("write", false, "apply edits instead of only reporting them")
	flag.Parse()

	root, err := findRoot()
	if err != nil {
		fail(err)
	}

	configPath := filepath.Join(root, "build", "config.yml")
	current, err := readConfigVersion(configPath)
	if err != nil {
		fail(err)
	}

	target := current
	if *set != "" {
		target = strings.TrimPrefix(*set, "v")
		if !semver(target) {
			fail(fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH", target))
		}
	}

	// build/config.yml is the source; the rest are derived. MSIX manifests use
	// a fourth version component, which is why those two carry the four flag.
	edits := []edit{
		{file: "build/config.yml", want: 1},
		{file: "build/darwin/Info.plist", want: 2, plist: true},
		{file: "build/darwin/Info.dev.plist", want: 2, plist: true},
		{file: "build/windows/info.json", want: 4},
		{file: "build/linux/nfpm/nfpm.yaml", want: 1},
		{file: "frontend/package.json", want: 1},
		{file: "frontend/package-lock.json", want: 2, limit: 2},
		{file: "build/windows/msix/app_manifest.xml", want: 1, four: true},
		{file: "build/windows/msix/template.xml", want: 1, four: true},
	}

	want := target
	changed := 0
	for _, e := range edits {
		path := filepath.Join(root, filepath.FromSlash(e.file))
		before, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		expected := want
		if e.four {
			expected += ".0"
		}
		limit := e.limit
		if limit == 0 {
			limit = e.want
		}
		after, found, total := rewriteVersionLines(string(before), expected, e.plist, limit)

		// total counts the version lines in scope; it must match what this
		// file is known to contain. In check mode every one of them must
		// already hold the target version; in set mode a shortfall is the
		// drift we are about to fix, so it is fine.
		checkOnly := *set == "" && !*write
		if total != e.want || (checkOnly && found != e.want) {
			fail(fmt.Errorf("%s: expected %d version line(s) at %q, found %d (of %d in scope)",
				e.file, e.want, expected, found, total))
		}
		if string(before) == after {
			continue
		}
		changed++
		fmt.Printf("%s: -> %s\n", e.file, expected)
		if *write {
			if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
				fail(err)
			}
		}
	}

	if changed == 0 {
		fmt.Printf("all files agree on %s\n", target)
		return
	}
	if !*write {
		fmt.Printf("\n%d file(s) out of date. Re-run with -write to apply.\n", changed)
		os.Exit(1)
	}
	fmt.Printf("\nupdated %d file(s) to %s\n", changed, target)
}

// rewriteVersionLines rewrites only version-assignment lines, leaving prose
// that happens to mention a version untouched. It returns the rewritten text,
// how many lines already carried want, and how many version lines exist in
// total. The caller compares both numbers to catch stale or malformed files.
func rewriteVersionLines(src, want string, plist bool, limit int) (string, int, int) {
	lines := strings.SplitAfter(src, "\n")
	found, total := 0, 0
	versionKey := false
	for i, line := range lines {
		if limit > 0 && total >= limit {
			break
		}
		trimmed := strings.TrimSpace(line)
		if plist {
			if strings.HasPrefix(trimmed, "<key>") {
				versionKey = trimmed == "<key>CFBundleShortVersionString</key>" ||
					trimmed == "<key>CFBundleVersion</key>"
			}
			if !versionKey || !strings.HasPrefix(trimmed, "<string>") {
				continue
			}
			versionKey = false
		} else if !isVersionContext(line) {
			continue
		}
		old, ok := versionValue(line)
		if !ok {
			continue
		}
		total++
		if old == want {
			found++
			continue
		}
		// plist values are wrapped in <string>, everything else in quotes.
		open, close := `"`, `"`
		if plist {
			open, close = "<string>", "</string>"
		}
		lines[i] = strings.Replace(line, open+old+close, open+want+close, 1)
	}
	return strings.Join(lines, ""), found, total
}

// versionValue extracts the quoted version from a version-assignment line.
// It handles YAML (`version: "x"`), JSON (`"version": "x"`), plist
// (`<string>x</string>`) and MSIX (`Version="x"`) forms.
func versionValue(line string) (string, bool) {
	s := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(s, "<string>"):
		end := strings.Index(s, "</string>")
		if end < 0 {
			return "", false
		}
		return s[len("<string>"):end], true
	case strings.HasPrefix(s, "Version=\""):
		rest := s[len("Version=\""):]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return "", false
		}
		return rest[:end], true
	default:
		// JSON/YAML: take the quoted value after the first colon, not the
		// quoted key. Lines look like `"file_version": "0.1.0",`.
		colon := strings.IndexByte(s, ':')
		first := strings.IndexByte(s[colon+1:], '"')
		if colon < 0 || first < 0 {
			return "", false
		}
		first += colon + 1
		last := strings.LastIndexByte(s, '"')
		if last <= first {
			return "", false
		}
		return s[first+1 : last], true
	}
}

func isVersionContext(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "version:") ||
		strings.HasPrefix(trimmed, `"version":`) ||
		strings.HasPrefix(trimmed, `"file_version"`) ||
		strings.HasPrefix(trimmed, `"product_version"`) ||
		strings.HasPrefix(trimmed, `"FileVersion"`) ||
		strings.HasPrefix(trimmed, `"ProductVersion"`) ||
		strings.HasPrefix(trimmed, "Version=")
}

func readConfigVersion(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if m := versionLine.FindStringSubmatch(line); m != nil {
			return m[2], nil
		}
	}
	return "", fmt.Errorf("%s: no info.version found", path)
}

func semver(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
		if n, err := strconv.Atoi(p); err != nil || n < 0 {
			return false
		}
	}
	return true
}

func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "build", "config.yml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cannot find repo root (build/config.yml)")
		}
		dir = parent
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "syncversion:", err)
	os.Exit(1)
}
