package sched_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// adr: 20261110210000 — the owner grants a peer an app's secrets before
// placing it there, reuses a fresh grant, renews while the app runs there
// and forgets it once the app left.

type fakeGrantRPC struct {
	calls []string
	ttl   time.Duration // the expiry it reports, from now
	err   error
}

func (f *fakeGrantRPC) GrantPeer(_ context.Context, appID, nodeName string, _ time.Duration) (time.Time, error) {
	f.calls = append(f.calls, appID+"@"+nodeName)
	if f.err != nil {
		return time.Time{}, f.err
	}
	return time.Now().Add(f.ttl), nil
}

func newGrantor(rpc *fakeGrantRPC, live map[string]bool) *sched.SecretGrantor {
	names := map[string]string{"n-own": "own.faas", "n-peer": "peer.faas", "n-other": "other.faas"}
	return sched.NewSecretGrantor(rpc, "n-own",
		func(id string) (string, bool) { n, ok := names[id]; return n, ok },
		func(appID, nodeID string) bool { return live[appID+"/"+nodeID] },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSecretGrantorEnsure(t *testing.T) {
	ctx := context.Background()
	rpc := &fakeGrantRPC{ttl: time.Hour}
	g := newGrantor(rpc, nil)

	g.Ensure(ctx, "n-own", "app-1")     // the owner needs no grant
	g.Ensure(ctx, "n-peer", "")         // no app, nothing to grant
	g.Ensure(ctx, "n-unknown", "app-1") // no name, refused locally
	if len(rpc.calls) != 0 {
		t.Fatalf("calls = %v, want none", rpc.calls)
	}
	g.Ensure(ctx, "n-peer", "app-1")
	g.Ensure(ctx, "n-peer", "app-1") // a fresh grant is reused
	if strings.Join(rpc.calls, ",") != "app-1@peer.faas" {
		t.Fatalf("calls = %v", rpc.calls)
	}

	// A grant with less than half its lifetime left is renewed on use.
	short := &fakeGrantRPC{ttl: 10 * time.Minute}
	g = newGrantor(short, nil)
	g.Ensure(ctx, "n-peer", "app-1")
	g.Ensure(ctx, "n-peer", "app-1")
	if len(short.calls) != 2 {
		t.Fatalf("short-lived grant calls = %v, want 2", short.calls)
	}

	// A refused grant is not remembered: the next placement asks again.
	refused := &fakeGrantRPC{err: errors.New("permission denied")}
	g = newGrantor(refused, nil)
	g.Ensure(ctx, "n-peer", "app-1")
	g.Ensure(ctx, "n-peer", "app-1")
	if len(refused.calls) != 2 {
		t.Fatalf("refused grant calls = %v, want 2", refused.calls)
	}
}

// stubVMM answers CreateColdBoot; any other method panics (unused here).
type stubVMM struct{ sched.VMM }

func (stubVMM) CreateColdBoot(context.Context, string, sched.AppSpec) (*sched.WakeOutcome, error) {
	return &sched.WakeOutcome{}, nil
}

// The router grants before it creates on a peer, and never for its own node.
func TestVMMRouterGrantsPeerBeforeCreate(t *testing.T) {
	ctx := context.Background()
	rpc := &fakeGrantRPC{ttl: time.Hour}
	nodes := []sched.ComputeNodeInfo{
		{ID: "n-own", Name: "own.faas", TargetURL: "unix:///run/faas/own.sock"},
		{ID: "n-peer", Name: "peer.faas", TargetURL: "unix:///run/faas/peer.sock"},
	}
	r := sched.NewVMMRouter(nodes, func(context.Context, string, *tls.Config) (sched.VMM, error) { return stubVMM{}, nil }, nil)
	r.WithSecretGrantor(sched.NewSecretGrantor(rpc, "n-own", r.NodeName, nil, slog.New(slog.NewTextHandler(io.Discard, nil))))

	if _, err := r.CreateColdBoot(ctx, "n-own", "i-1", sched.AppSpec{AppID: "app-1"}); err != nil {
		t.Fatal(err)
	}
	if len(rpc.calls) != 0 {
		t.Fatalf("own-node create granted: %v", rpc.calls)
	}
	if _, err := r.CreateColdBoot(ctx, "n-peer", "i-2", sched.AppSpec{AppID: "app-1"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rpc.calls, ",") != "app-1@peer.faas" {
		t.Fatalf("peer create grants = %v", rpc.calls)
	}
	// A failed grant does not block the create (a peer that opens locally
	// needs none).
	rpc.err = errors.New("unavailable")
	if _, err := r.CreateColdBoot(ctx, "n-peer", "i-3", sched.AppSpec{AppID: "app-2"}); err != nil {
		t.Fatalf("create after a failed grant = %v", err)
	}
}

func TestSecretGrantorRenewKeepsOnlyLivePeers(t *testing.T) {
	ctx := context.Background()
	rpc := &fakeGrantRPC{ttl: time.Hour}
	live := map[string]bool{"app-1/n-peer": true}
	g := newGrantor(rpc, live)
	g.Ensure(ctx, "n-peer", "app-1")
	g.Ensure(ctx, "n-other", "app-2")
	rpc.calls = nil

	g.Renew(ctx) // app-1 still runs on the peer; app-2 left the other node
	if strings.Join(rpc.calls, ",") != "app-1@peer.faas" {
		t.Fatalf("renew calls = %v", rpc.calls)
	}
	rpc.calls = nil
	live["app-1/n-peer"] = false
	g.Renew(ctx)
	g.Renew(ctx)
	if len(rpc.calls) != 0 {
		t.Fatalf("calls after the app left = %v, want none", rpc.calls)
	}

	// A nil grantor (secretd off) is a no-op.
	var off *sched.SecretGrantor
	off.Ensure(ctx, "n-peer", "app-1")
	off.Renew(ctx)
}
