//go:build !windows

package transfer

import "syscall"

// quietProc is a no-op off Windows: a console child of a console process does
// not pop a new window.
func quietProc() *syscall.SysProcAttr {
	return nil
}
