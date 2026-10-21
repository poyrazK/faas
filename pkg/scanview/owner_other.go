//go:build !linux && !darwin

package scanview

import "os"

func projectionOwner(*os.Root) (func(string) error, error) {
	return nil, ErrInvalid
}
