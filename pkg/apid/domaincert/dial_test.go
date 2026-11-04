package domaincert_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/domaincert"
)

func TestDialFallsBackWithinBudgetWithoutResolvingAgain(t *testing.T) {
	addresses := []net.IPAddr{{IP: net.ParseIP("2001:4860:4860::8888")}, {IP: net.ParseIP("8.8.8.8")}}
	var attempts []string
	client, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn, err := domaincert.Dial(t.Context(), addresses, 100*time.Millisecond, func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts = append(attempts, address)
		if len(attempts) == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if len(attempts) != 2 || attempts[0] != "[2001:4860:4860::8888]:443" || attempts[1] != "8.8.8.8:443" {
		t.Fatalf("numeric attempts=%v", attempts)
	}
}

func TestDialHonorsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := domaincert.Dial(ctx, []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, time.Second, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial called after cancellation")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dial=%v", err)
	}
}
