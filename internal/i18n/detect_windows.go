//go:build windows

package i18n

import "golang.org/x/sys/windows"

var procGetUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").
	NewProc("GetUserDefaultUILanguage")

// Detect returns the language of the Windows UI. The LANGID packs a primary
// language in the low 10 bits and a sublanguage above it; only the primary
// matters here because the app ships one Chinese variant.
func Detect() Lang {
	r, _, _ := procGetUserDefaultUILanguage.Call()
	const langChinese = 0x04
	if uint16(r)&0x3ff == langChinese {
		return ZHCN
	}
	return EN
}
