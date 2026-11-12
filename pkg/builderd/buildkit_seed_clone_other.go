//go:build !linux

package builderd

import "errors"

// cloneFile needs FICLONE, which only Linux builder nodes have.
func cloneFile(_, _ string) error { return errors.New("reflink copy needs linux") }
