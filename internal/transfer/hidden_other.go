//go:build !windows

package transfer

import "io/fs"

// isHiddenInfo always reports false: Unix filesystems have no hidden attribute.
// A leading dot is the only convention there, and that is handled by the
// separate ShowDotfiles option.
func isHiddenInfo(fs.FileInfo) bool { return false }
