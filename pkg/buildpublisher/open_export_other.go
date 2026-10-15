//go:build !unix

package buildpublisher

import "os"

// Native source conversion runs on Unix hosts. This portable fallback keeps
// metadata/CLI imports buildable and checks both identity and type on open.
func openExportFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, ErrInvalid
	}
	return f, nil
}
