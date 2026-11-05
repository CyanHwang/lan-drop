package i18n

import "testing"

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want Lang
		ok   bool
	}{
		{"en", EN, true},
		{"en-US", EN, true},
		{"EN_gb.UTF-8", EN, true},
		{"zh", ZHCN, true},
		{"zh-CN", ZHCN, true},
		{"zh_CN.UTF-8", ZHCN, true},
		{"zh-Hans", ZHCN, true},
		{"zh-TW", ZHCN, true},
		{"ja", "", false},
		{"", "", false},
		{"auto", "", false},
	}
	for _, c := range cases {
		got, ok := Normalize(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Normalize(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestResolveFallsBackToSystem(t *testing.T) {
	// An explicit preference always wins.
	if got := Resolve("zh-CN"); got != ZHCN {
		t.Errorf("Resolve(zh-CN) = %q", got)
	}
	if got := Resolve("en"); got != EN {
		t.Errorf("Resolve(en) = %q", got)
	}
	// Anything else (including "auto") resolves to a supported language.
	for _, pref := range []string{"auto", "", "klingon"} {
		if got := Resolve(pref); got != EN && got != ZHCN {
			t.Errorf("Resolve(%q) = %q, want a supported language", pref, got)
		}
	}
}

func TestTranslationFallsBackToKey(t *testing.T) {
	if got := T(EN, Msg("no.such.key")); got != "no.such.key" {
		t.Errorf("missing key = %q, want the key itself", got)
	}
}

func TestFormatArgs(t *testing.T) {
	got := T(EN, ErrSharePathInvalid, "Photos")
	want := "Photos: invalid path"
	if got != want {
		t.Errorf("T() = %q, want %q", got, want)
	}
}

// Every key must exist in both languages. A half-translated catalogue shows up
// as an English string in the Chinese UI, which is easy to miss by eye.
func TestCatalogueIsComplete(t *testing.T) {
	en, zh := catalogue[EN], catalogue[ZHCN]
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("key %q is missing from zh-CN", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q is missing from en", k)
		}
	}
}

func TestCatalogueHasNoEmptyStrings(t *testing.T) {
	for lang, msgs := range catalogue {
		for k, v := range msgs {
			if v == "" {
				t.Errorf("%s: key %q is empty", lang, k)
			}
		}
	}
}

func TestSupportedList(t *testing.T) {
	if len(Supported) != 2 {
		t.Fatalf("Supported = %v", Supported)
	}
	for _, l := range Supported {
		if _, ok := catalogue[l]; !ok {
			t.Errorf("Supported language %q has no catalogue", l)
		}
	}
}
