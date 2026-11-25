//go:build !windows

package transfer

import "golang.org/x/sys/unix"

// freeSpace reports how many bytes the calling user may still write to the
// filesystem that holds dir. Bavail, not Bfree, because the reserved blocks
// are exactly the ones a normal user cannot touch.
func freeSpace(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
