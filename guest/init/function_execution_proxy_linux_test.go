//go:build linux

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

func TestFunctionExecutionProxyAuthenticatesOwnerAndHostAcknowledgement(t *testing.T) {
	var mu sync.Mutex
	var observed []functionlifetime.Request
	p := &functionExecutionProxy{dial: func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			defer func() { _ = b.Close() }()
			req, err := functionlifetime.ReadRequest(b)
			if err != nil {
				return
			}
			mu.Lock()
			observed = append(observed, req)
			mu.Unlock()
			_ = functionlifetime.Write(b, functionlifetime.Reply{Version: functionlifetime.Version, Status: functionlifetime.Busy, Plan: api.PlanFree})
		}()
		return a, nil
	}}
	if err := p.bind(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "leases.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve(ln)
	dial := func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", path) }
	req := functionlifetime.Request{Version: functionlifetime.Version, Kind: functionlifetime.Begin, Sequence: 1, BudgetMS: 1000}
	reply, err := functionlifetime.Call(t.Context(), dial, req)
	if err != nil || reply.Status != functionlifetime.Busy {
		t.Fatal("proxy invented a successful host acknowledgement")
	}
	mu.Lock()
	first := observed[0]
	mu.Unlock()
	if first.Owner != 1 {
		t.Fatal("guest-init did not stamp its process generation")
	}
	req.Owner = 99
	reply, err = functionlifetime.Call(t.Context(), dial, req)
	if err != nil || reply.Status != functionlifetime.Stale {
		t.Fatal("application forged an owner generation")
	}
	req.Owner = 0
	p.mu.Lock()
	p.pid = os.Getpid() + 1
	p.mu.Unlock()
	reply, err = functionlifetime.Call(t.Context(), dial, req)
	if err != nil || reply.Status != functionlifetime.Stale {
		t.Fatal("unowned peer PID reached the host")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 {
		t.Fatal("rejected local requests crossed the VM boundary")
	}
}
