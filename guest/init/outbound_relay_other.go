//go:build !linux

package main

import (
	"errors"
	"log/slog"
)

func startOutboundRelay(*slog.Logger) error {
	return errors.New("outbound relay requires Linux AF_VSOCK")
}
