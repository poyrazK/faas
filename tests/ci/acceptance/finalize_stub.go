//go:build !metal

package acceptance

import (
	"context"
	"errors"
)

func Finalize(context.Context, string, string) error {
	return errors.New("CI acceptance finalization requires the metal-tagged helper on the dedicated host")
}
