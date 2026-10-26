//go:build !unix

package devpatch

import "io/fs"

// singleLink cannot read a link count here, so every write takes the staged
// rename.
func singleLink(fs.FileInfo) bool { return false }
