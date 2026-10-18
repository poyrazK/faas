//go:build !linux

package fcvm

import (
	"context"
	"errors"
)

func nativeRepairProducerSupported() bool { return false }

func nativeProducerExited(context.Context, nativeProducerIdentity) error {
	return errors.New("native producer: daemon exit proof requires Linux pidfds")
}
