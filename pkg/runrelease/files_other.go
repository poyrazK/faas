//go:build !linux && !darwin

package runrelease

import (
	"errors"
	"os"
)

func openInput(string) (*os.File, error) {
	return nil, errors.New("release checks require Linux (or macOS for portable verification)")
}
