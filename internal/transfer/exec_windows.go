//go:build windows

package transfer

import "syscall"

// quietProc keeps ffmpeg from flashing a console window on screen. The desktop
// app is a GUI process; without CREATE_NO_WINDOW every preview would briefly
// open a black terminal on the user's desktop.
func quietProc() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		HideWindow:    true,
	}
}
