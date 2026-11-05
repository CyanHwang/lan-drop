package transfer

import (
	"path"
	"regexp"
	"strings"
)

// The filter field is free text typed by a human, so parsing is deliberately
// forgiving: anything that cannot be understood is dropped, never reported.
//
// Accepted forms:
//
//	*.tmp, node_modules, .git      comma / semicolon / pipe separated globs
//	*.tmp; node_modules            same, other separators
//	^\.(git|svn)$                  a single expression that only reads as regex
//	re:^\.(git|svn)$               explicit regex, always wins
//
// A piece is treated as a glob by default. It only becomes a regex when the
// whole input is one expression and contains characters that mean something
// in a regex but nothing in a filename glob (^ $ ( ) [ ] { } + \). That keeps
// "*.tmp" a glob — as a regex it would not even compile — while "^\.git$"
// still works without a prefix.

// regexPrefix forces the following expression to be parsed as a regex.
const regexPrefix = "re:"

// filterSeparators split a multi-entry filter. All of them are characters that
// cannot appear in a filename on Windows, so there is no ambiguity with a real
// name. Spaces are not separators: "My File.txt" is a legitimate name.
//
// The pipe is deliberately absent: it is the regex alternation operator, and
// "^(a|b)$" would otherwise be torn apart before it could be compiled.
const filterSeparators = ",;\r\n"

type filterRule struct {
	// Exactly one of these is set.
	glob string
	re   *regexp.Regexp
}

// filterSet is the compiled form of the filter field.
type filterSet struct {
	rules []filterRule
}

func (f filterSet) empty() bool { return len(f.rules) == 0 }

// Match reports whether name should be filtered out. Matching is
// case-insensitive on every platform: a filter is a convenience, and having it
// behave differently per OS is more surprising than being lenient.
func (f filterSet) Match(name string) bool {
	if len(f.rules) == 0 {
		return false
	}
	lower := strings.ToLower(name)
	for _, r := range f.rules {
		if r.re != nil {
			if r.re.MatchString(name) {
				return true
			}
			continue
		}
		if ok, err := path.Match(r.glob, lower); err == nil && ok {
			return true
		}
	}
	return false
}

// ParseFilter compiles a user-supplied filter string. Entries that do not
// compile are silently skipped.
func ParseFilter(raw string) filterSet {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return filterSet{}
	}

	// A leading prefix with no separators means the whole input is one regex,
	// which is how an expression containing a comma can still be written.
	// With separators present it applies per entry instead, so a typo in one
	// entry cannot discard the rest of the list.
	if strings.HasPrefix(strings.ToLower(raw), regexPrefix) &&
		!strings.ContainsAny(raw, filterSeparators) {
		if re, err := compileRegex(raw[len(regexPrefix):]); err == nil {
			return filterSet{rules: []filterRule{{re: re}}}
		}
		return filterSet{}
	}

	rules := make([]filterRule, 0, 4)
	for _, piece := range strings.FieldsFunc(raw, func(r rune) bool {
		return strings.ContainsRune(filterSeparators, r)
	}) {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}

		// Per-entry prefix, so a list can mix globs and regexes.
		if strings.HasPrefix(strings.ToLower(piece), regexPrefix) {
			if re, err := compileRegex(piece[len(regexPrefix):]); err == nil {
				rules = append(rules, filterRule{re: re})
			}
			continue
		}

		if looksLikeRegex(piece) {
			if re, err := compileRegex(piece); err == nil {
				rules = append(rules, filterRule{re: re})
				continue
			}
			// Falls through to glob: a stray "(" is a legal filename character.
		}

		glob := strings.ToLower(piece)
		// Reject malformed globs up front ("a[b") instead of on every entry.
		if _, err := path.Match(glob, ""); err != nil {
			continue
		}
		rules = append(rules, filterRule{glob: glob})
	}
	return filterSet{rules: rules}
}

func compileRegex(expr string) (*regexp.Regexp, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, errEmptyExpr
	}
	// Case-insensitive to match the glob behaviour.
	return regexp.Compile("(?i)" + expr)
}

// looksLikeRegex reports whether s contains a character that is meaningful in
// a regex but not in a filename glob. Deliberately excludes "*" and "?" — they
// are glob wildcards too, and "*.tmp" is far more likely than a regex.
func looksLikeRegex(s string) bool {
	return strings.ContainsAny(s, `^$()[]{}+\`)
}

type filterError string

func (e filterError) Error() string { return string(e) }

const errEmptyExpr = filterError("empty expression")
