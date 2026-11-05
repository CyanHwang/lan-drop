//go:build !windows && !darwin

package i18n

import (
	"os"
	"strings"
)

// Detect walks the standard POSIX locale variables in priority order.
// LANGUAGE is a colon-separated fallback list; the rest are single values.
func Detect() Lang {
	for _, key := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		for _, part := range strings.Split(v, ":") {
			if l, ok := Normalize(part); ok {
				return l
			}
		}
	}
	return EN
}
