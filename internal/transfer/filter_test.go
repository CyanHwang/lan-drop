package transfer

import "testing"

func TestParseFilterGlobs(t *testing.T) {
	cases := []struct {
		filter string
		match  []string
		pass   []string
	}{
		{"", nil, []string{"anything", "*.tmp"}},
		{"*.tmp", []string{"a.tmp", "A.TMP"}, []string{"a.txt"}},
		{"*.tmp, node_modules", []string{"a.tmp", "node_modules"}, []string{"a.txt", "nodes"}},
		{"*.tmp;node_modules,.git", []string{"a.tmp", "node_modules", ".git"}, []string{"a.txt"}},
		{"  *.tmp ,  .git  ", []string{"a.tmp", ".git"}, []string{"a.txt"}},
		{"My File.txt", []string{"My File.txt"}, []string{"My", "File.txt"}},
		{"?.log", []string{"a.log"}, []string{"ab.log"}},
	}

	for _, c := range cases {
		f := ParseFilter(c.filter)
		for _, name := range c.match {
			if !f.Match(name) {
				t.Errorf("filter %q should match %q", c.filter, name)
			}
		}
		for _, name := range c.pass {
			if f.Match(name) {
				t.Errorf("filter %q should not match %q", c.filter, name)
			}
		}
	}
}

func TestParseFilterRegex(t *testing.T) {
	// A single expression containing regex-only characters is taken as regex
	// even without the prefix.
	f := ParseFilter(`^\.(git|svn)$`)
	if !f.Match(".git") || !f.Match(".svn") {
		t.Fatal("regex form did not match")
	}
	if f.Match("git") || f.Match("x.git") {
		t.Fatal("regex form matched too much")
	}

	// The explicit prefix always wins.
	f = ParseFilter(`re:\.log$`)
	if !f.Match("server.log") || f.Match("server.log.1") {
		t.Fatal("prefixed regex did not behave as anchored-at-end")
	}
}

func TestParseFilterCaseInsensitive(t *testing.T) {
	f := ParseFilter("Node_Modules")
	if !f.Match("node_modules") || !f.Match("NODE_MODULES") {
		t.Fatal("glob matching should ignore case")
	}

	f = ParseFilter("^BUILD$")
	if !f.Match("build") {
		t.Fatal("regex matching should ignore case")
	}
}

// The field is free text. A typo must never surface as an error, and must
// never take the rest of the filter down with it.
func TestParseFilterIgnoresInvalidEntries(t *testing.T) {
	cases := []string{
		`re:[`,         // unterminated character class
		`re:(`,         // unterminated group
		`a[b`,          // malformed glob
		`*, a[b, .git`, // one bad piece among good ones
	}
	for _, raw := range cases {
		f := ParseFilter(raw) // must not panic
		_ = f.Match("whatever")
	}

	// The valid part of a mixed list still works.
	f := ParseFilter(`*, a[b, .git`)
	if !f.Match(".git") {
		t.Fatal("valid entry was dropped along with the invalid one")
	}
}

func TestParseFilterSeparatorInsideName(t *testing.T) {
	// Spaces are not separators, so a name with a space stays one entry.
	f := ParseFilter("my file.txt")
	if !f.Match("my file.txt") {
		t.Fatal("space should not split a name")
	}
}

// A bad entry must never take the rest of the list with it — including when
// the list starts with a prefixed regex that fails to compile.
func TestParseFilterBadEntryDoesNotDropRest(t *testing.T) {
	f := ParseFilter(`re:[(, *.tmp`)
	if !f.Match("a.tmp") {
		t.Fatal("valid glob after a broken regex entry was dropped")
	}
	if f.Match("a.txt") {
		t.Fatal("unrelated name matched")
	}
}
