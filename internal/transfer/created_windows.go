//go:build windows

package transfer

import (
	"os"
	"syscall"
	"time"
)

// fileCreated reads the NTFS creation time. The Manage Engine has it on every
// file, so the viewer can show a real "created" value on the platform the
// desktop app ships on.
func fileCreated(abs string, info os.FileInfo) time.Time {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, data.CreationTime.Nanoseconds())
}
