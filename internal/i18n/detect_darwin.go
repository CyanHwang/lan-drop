//go:build darwin

package i18n

import (
	"os/exec"
	"strings"
)

// Detect reads the preferred UI languages from the global defaults domain. The
// value is a plist array; the first entry is the user's top choice.
func Detect() Lang {
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return EN
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, `",()`)
		if line == "" {
			continue
		}
		if l, ok := Normalize(line); ok {
			return l
		}
	}
	return EN
}
