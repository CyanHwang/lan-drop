//go:build windows

package transfer

import "golang.org/x/sys/windows"

// freeSpace reports how many bytes the calling user may still write to the
// volume that holds dir. The Win32 call already accounts for quotas and
// compressed volumes, which is why it beats total-size-minus-used arithmetic.
func freeSpace(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}
