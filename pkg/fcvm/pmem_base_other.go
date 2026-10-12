//go:build !linux

package fcvm

import "errors"

func reflinkFile(_, _ string) error { return errors.New("reflink clone requires Linux") }
