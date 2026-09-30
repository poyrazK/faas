package devbridge

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestReconnectLeaseAndTerminalAuthorization(t *testing.T) {
	attempts, serves := 0, 0
	err := RunReconnecting(t.Context(), func(context.Context) (net.Conn, error) {
		attempts++
		if attempts == 3 {
			return nil, ErrUnauthorized
		}
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}, func(context.Context, net.Conn) error { serves++; return ErrDisconnected }, nil)
	if !errors.Is(err, ErrUnauthorized) || attempts != 3 || serves != 2 {
		t.Fatalf("attempts=%d serves=%d err=%v", attempts, serves, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_ = RunReconnecting(ctx, func(context.Context) (net.Conn, error) { cancel(); return nil, ErrDisconnected }, func(context.Context, net.Conn) error { t.Fatal("served cancelled session"); return nil }, nil)
}
