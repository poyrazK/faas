//go:build !linux

package main

import (
	"context"
	"fmt"
	"net"
)

func dialCustomerFixtureVSock(context.Context, string, string) (net.Conn, error) {
	return nil, fmt.Errorf("native fixture requires Linux vsock")
}
