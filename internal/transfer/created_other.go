//go:build !windows && !linux

package transfer

import (
	"os"
	"time"
)

// fileCreated has no portable implementation here. A missing value is shown as
// blank in the viewer, which is better than presenting the inode change time
// as if it were the file's birthday.
func fileCreated(abs string, info os.FileInfo) time.Time {
	return time.Time{}
}
