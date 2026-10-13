//go:build !linux

package fcvm

import (
	"errors"
	"os"
	"time"
)

func reflinkFile(_, _ string) error { return errors.New("reflink clone requires Linux") }

func lastUse(info os.FileInfo) time.Time { return info.ModTime() }
