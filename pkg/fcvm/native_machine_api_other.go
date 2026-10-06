//go:build !linux

package fcvm

import (
	"context"
	"errors"
	"net"
)

func dialNativeMachine(context.Context, string, nativeLaunchRecord) (net.Conn, error) {
	return nil, errors.New("native machine: credential-bound API requires Linux")
}
