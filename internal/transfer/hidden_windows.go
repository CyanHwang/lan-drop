//go:build windows

package transfer

import (
	"io/fs"
	"syscall"
)

// isHiddenInfo reports whether the entry carries the Windows hidden attribute.
// The data comes from the FileInfo that ReadDir already produced, so checking
// it costs no extra system call.
func isHiddenInfo(info fs.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return data.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
}
