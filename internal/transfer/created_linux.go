//go:build linux

package transfer

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// fileCreated uses statx's birth time when the filesystem provides it. ext4
// and btrfs do; tmpfs and some network filesystems do not, in which case there
// is honestly no creation time to show and the viewer leaves the field blank.
func fileCreated(abs string, info os.FileInfo) time.Time {
	var st unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, abs, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &st); err != nil {
		return time.Time{}
	}
	if st.Mask&unix.STATX_BTIME == 0 {
		return time.Time{}
	}
	return time.Unix(st.Btime.Sec, int64(st.Btime.Nsec))
}
