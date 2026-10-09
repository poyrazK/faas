//go:build !linux

package main

import (
	"fmt"
	"net"
)

type rootListener struct{ net.Listener }

func (l rootListener) Accept() (net.Conn, error) {
	return nil, fmt.Errorf("profile peer authentication requires Linux")
}
