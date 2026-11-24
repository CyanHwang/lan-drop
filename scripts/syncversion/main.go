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
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var versionLine = regexp.MustCompile(`^(\s*version:\s*)"([^"]+)"(\s*(?:#.*)?)$`)

type edit struct {
	file string
	// kind selects the line shape to rewrite:
	//   ""          quoted value after a colon (YAML/JSON)
	//   "plist"     <string> after a CFBundle version key
	//   "plistdev"  same, but CFBundleShortVersionString keeps a -dev suffix
	//   "define"    NSIS `!define NAME "x"`
	//   "manifest"  Windows `assemblyIdentity ... version="x"`
	//   "four"      MSIX Version="x.0"
	kind string
	// want is how many version-bearing lines in this file should carry the
	// target version. Checking this count catches both stale and partial edits.
	want int
	// limit caps how many leading version lines belong to this file. Needed
	// for package-lock.json, whose dependency entries also carry "version".
	limit int
	// optional marks files that may be absent, such as platform directories
	// excluded by .gitignore. A missing optional file is skipped; a missing
	// required one is an error.
	optional bool
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

	// build/config.yml is the source; every other file is derived.
	edits := []edit{
		{file: "build/config.yml", want: 1},
		{file: "build/darwin/Info.plist", kind: "plist", want: 2},
		{file: "build/darwin/Info.dev.plist", kind: "plist", want: 2},
		{file: "build/ios/Info.plist", kind: "plist", want: 2, optional: true},
		{file: "build/ios/Info.dev.plist", kind: "plistdev", want: 2, optional: true},
		{file: "build/windows/info.json", want: 4},
		{file: "build/windows/wails.exe.manifest", kind: "manifest", want: 1},
		{file: "build/windows/nsis/wails_tools.nsh", kind: "define", want: 1},
		{file: "build/linux/nfpm/nfpm.yaml", want: 1},
		{file: "frontend/package.json", want: 1},
		{file: "frontend/package-lock.json", want: 2, limit: 2},
		{file: "build/windows/msix/app_manifest.xml", kind: "four", want: 1},
		{file: "build/windows/msix/template.xml", kind: "four", want: 1},
	}

	want := target
	changed := 0
	for _, e := range edits {
		path := filepath.Join(root, filepath.FromSlash(e.file))
		before, err := os.ReadFile(path)
		if err != nil {
			if e.optional && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			fail(err)
		}
		expected := want
		if e.kind == "four" {
			expected += ".0"
		}
		limit := e.limit
		if limit == 0 {
			limit = e.want
		}
		after, found, total := rewriteVersionLines(string(before), expected, e.kind, limit)

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
func rewriteVersionLines(src, want, kind string, limit int) (string, int, int) {
	lines := strings.SplitAfter(src, "\n")
	found, total := 0, 0
	versionKey := ""
	for i, line := range lines {
		if limit > 0 && total >= limit {
			break
		}
		trimmed := strings.TrimSpace(line)
		old, open, close, ok := "", `"`, `"`, false

		switch kind {
		case "plist", "plistdev":
			if strings.HasPrefix(trimmed, "<key>") {
				versionKey = versionPlistKey(trimmed)
			}
			if versionKey == "" || !strings.HasPrefix(trimmed, "<string>") {
				continue
			}
			end := strings.Index(trimmed, "</string>")
			if end < 0 {
				continue
			}
			old, open, close, ok = trimmed[len("<string>"):end], "<string>", "</string>", true
			versionKey = ""
		case "define":
			if !strings.HasPrefix(trimmed, "!define INFO_PRODUCTVERSION ") {
				continue
			}
			old, ok = quotedValue(trimmed)
		case "manifest":
			if !strings.HasPrefix(trimmed, "<assemblyIdentity") || !strings.Contains(trimmed, "version=") {
				continue
			}
			old, ok = attrValue(trimmed, "version")
		case "four":
			if !strings.HasPrefix(trimmed, "Version=") {
				continue
			}
			old, ok = attrValue(trimmed, "Version")
		default:
			if !isVersionContext(line) {
				continue
			}
			old, ok = quotedValue(trimmed)
		}
		if !ok {
			continue
		}

		// A dev plist keeps its -dev suffix on the short version key.
		expected := want
		if kind == "plistdev" && strings.HasSuffix(old, "-dev") {
			expected += "-dev"
		}
		total++
		if old == expected {
			found++
			continue
		}
		lines[i] = strings.Replace(line, open+old+close, open+expected+close, 1)
	}
	return strings.Join(lines, ""), found, total
}

func versionPlistKey(s string) string {
	if s == "<key>CFBundleVersion</key>" {
		return "build"
	}
	if s == "<key>CFBundleShortVersionString</key>" {
		return "short"
	}
	return ""
}

// quotedValue returns the contents of the last quoted string on a line.
func quotedValue(s string) (string, bool) {
	last := strings.LastIndexByte(s, '"')
	if last < 0 {
		return "", false
	}
	first := strings.LastIndexByte(s[:last], '"')
	if first < 0 {
		return "", false
	}
	return s[first+1 : last], true
}

// attrValue returns the value of attribute="..." on a line.
func attrValue(s, attr string) (string, bool) {
	key := attr + `="`
	i := strings.Index(s, key)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

func isVersionContext(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "version:") ||
		strings.HasPrefix(trimmed, `"version":`) ||
		strings.HasPrefix(trimmed, `"file_version"`) ||
		strings.HasPrefix(trimmed, `"product_version"`) ||
		strings.HasPrefix(trimmed, `"FileVersion"`) ||
		strings.HasPrefix(trimmed, `"ProductVersion"`)
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
