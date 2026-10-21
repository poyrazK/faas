//go:build !linux

package netns

import (
	"errors"
	"net"
)

func pinPublicOutput(*net.Dialer, int) error {
	return errors.New("public forward output pin requires native Linux")
}

func CheckPublicForwardRouting() error {
	return errors.New("public forward routing requires native Linux")
}
