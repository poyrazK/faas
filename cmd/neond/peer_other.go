//go:build !linux

package main

import (
	"errors"
	"net"
)

// peerUID is unavailable off Linux, so neond admits no caller there.
func peerUID(net.Conn) (int, error) {
	return -1, errors.New("neond: peer credentials need Linux")
}
