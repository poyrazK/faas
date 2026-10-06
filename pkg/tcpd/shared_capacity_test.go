package tcpd

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorSharesConnectionLimitAcrossPorts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 2)
	var selections atomic.Int32
	routes := NewRouteTable()
	var rows []state.TCPListener
	for i, port := range []int{40124, 40125} {
		row := state.TCPListener{ID: fmt.Sprintf("listener-%d", i), AppID: fmt.Sprintf("app-%d", i), AccountID: fmt.Sprintf("account-%d", i), ListenerName: "echo", PublicPort: port, GuestPort: 8080, Protocol: "tcp", Enabled: true}
		route, err := routeFromListener(row)
		if err != nil {
			t.Fatal(err)
		}
		if err := routes.Upsert(route); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	type binding struct {
		port    int
		address string
	}
	bound := make(chan binding, 2)
	supervisor := &Supervisor{
		Source:                   supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) { return rows, nil }),
		Routes:                   routes,
		MaxConnections:           1,
		MaxConnectionsPerAccount: 1,
		RefreshInterval:          time.Hour,
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) {
			selections.Add(1)
			return gateway.Target{}, nil
		}),
		Forwarder: forwarderFunc(func(_ context.Context, conn net.Conn, _ gateway.Target) error {
			entered <- struct{}{}
			if _, err := io.WriteString(conn, "accepted"); err != nil {
				return err
			}
			_, err := io.Copy(io.Discard, conn)
			return err
		}),
		Listen: func(_, address string) (net.Listener, error) {
			_, rawPort, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			port, err := strconv.Atoi(rawPort)
			if err != nil {
				return nil, err
			}
			base, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			bound <- binding{port: port, address: base.Addr().String()}
			return &aliasedTCPListener{Listener: base, port: port}, nil
		},
	}
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("supervisor failed to stop")
		}
	}()
	var addresses []string
	for i := 0; i < 2; i++ {
		select {
		case b := <-bound:
			addresses = append(addresses, b.address)
		case <-time.After(2 * time.Second):
			t.Fatal("supervisor did not bind both listeners")
		}
	}
	first, err := net.Dial("tcp", addresses[0])
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first session not admitted")
	}
	second, err := net.Dial("tcp", addresses[1])
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	var timeout net.Error
	if _, err := second.Read(one[:]); err == nil {
		t.Fatal("excess session returned data")
	} else if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("excess session remained open")
	}
	select {
	case <-entered:
		t.Fatal("second port bypassed global cap")
	default:
	}
	if selections.Load() != 1 {
		t.Fatal("capacity rejection reached target selection or wake")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Observe admission over the socket rather than inspecting semaphore state.
	admitted := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		retry, err := net.DialTimeout("tcp", addresses[1], time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := retry.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			_ = retry.Close()
			t.Fatal(err)
		}
		var response [8]byte
		_, err = io.ReadFull(retry, response[:])
		_ = retry.Close()
		if err == nil {
			if string(response[:]) != "accepted" {
				t.Fatalf("unexpected admission response: %q", response)
			}
			admitted = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !admitted {
		t.Fatal("completed session did not release capacity for another listener")
	}
}
