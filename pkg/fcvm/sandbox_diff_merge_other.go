//go:build !linux

package fcvm

import (
	"context"
	"errors"
	"os"
)

func mergeDiffSnapshot(context.Context, *os.File, string, string) (int64, bool, error) {
	return 0, false, errors.New("diff snapshot merge requires Linux")
}
