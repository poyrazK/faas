//go:build !linux

package vmmdgrpc

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/sdunit"
)

func openUnitProcessReal(sdunit.Unit, string) (unitProcess, error) {
	return nil, errors.New("instance bridge units need Linux")
}
