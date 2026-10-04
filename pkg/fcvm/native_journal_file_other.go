//go:build !linux && !darwin

package fcvm

import (
	"context"
	"errors"
	"os"
)

func checkNativeJournalPath(_ string, _ bool) error {
	return errors.New("native launch journal requires supported file locking")
}

func openNativeJournalFile(_ string, _ int) (*os.File, error) {
	return nil, errors.New("native launch journal requires supported file locking")
}

func lockNativeJournalFile(_ context.Context, _ string) (*os.File, error) {
	return nil, errors.New("native launch journal requires supported file locking")
}
