// adr: 20261108124106
package sched_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// bindsTo reports whether cfg's handshake hook accepts a vmmd leaf with CN
// cn: the dial config must bind each dial to the node schedd means.
func bindsTo(t *testing.T, cfg *tls.Config, cn string) bool {
	t.Helper()
	if cfg == nil || cfg.VerifyPeerCertificate == nil {
		t.Fatal("the dial config has no node binding")
	}
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	return cfg.VerifyPeerCertificate(nil, [][]*x509.Certificate{{leaf}}) == nil
}

var errNoDial = errors.New("test: dial refused")

// The router binds each dial to the dialed node's name, and a refresh that
// renames the node moves the binding with it.
func TestVMMRouterBindsEachDialToTheNodeName(t *testing.T) {
	var (
		mu  sync.Mutex
		got *tls.Config
	)
	dial := func(_ context.Context, _ string, cfg *tls.Config) (sched.VMM, error) {
		mu.Lock()
		got = cfg
		mu.Unlock()
		return nil, errNoDial
	}
	shared := &tls.Config{MinVersion: tls.VersionTLS13}
	r := sched.NewVMMRouter([]sched.ComputeNodeInfo{{ID: "n1", Name: "node-a.faas", TargetURL: "tcp://192.0.2.1:50051"}}, dial, shared)

	if _, err := r.Ping(context.Background(), "n1"); err == nil {
		t.Fatal("ping through a refused dial succeeded")
	}
	if !bindsTo(t, got, "node-a.faas") || bindsTo(t, got, "node-b.faas") {
		t.Fatal("the dial to node-a is not bound to node-a's leaf alone")
	}

	// A target change through the name-less Refresh keeps the name.
	r.Refresh("n1", "tcp://192.0.2.2:50051")
	_, _ = r.Ping(context.Background(), "n1")
	if !bindsTo(t, got, "node-a.faas") {
		t.Fatal("Refresh dropped the node's name")
	}
	r.RefreshNode("n1", "tcp://192.0.2.3:50051", "node-b.faas")
	_, _ = r.Ping(context.Background(), "n1")
	if !bindsTo(t, got, "node-b.faas") || bindsTo(t, got, "node-a.faas") {
		t.Fatal("RefreshNode did not move the binding to the new name")
	}
	if shared.VerifyPeerCertificate != nil {
		t.Fatal("binding a dial changed the shared config")
	}
}

// The heartbeat probe dials each node bound to that node's name.
func TestHeartbeatBindsTheProbeToTheNodeName(t *testing.T) {
	store := state.NewMemStore()
	nodes, err := store.ActiveComputeNodes(context.Background())
	if err != nil || len(nodes) != 1 {
		t.Fatalf("seeded nodes = %v, %v; want one", nodes, err)
	}
	var (
		mu  sync.Mutex
		got *tls.Config
	)
	dialer := sched.HeartbeatDialerFunc(func(_ context.Context, _ string, cfg *tls.Config) (sched.VMM, error) {
		mu.Lock()
		got = cfg
		mu.Unlock()
		return nil, errNoDial
	})
	h := sched.NewHeartbeat(store, dialer, &tls.Config{MinVersion: tls.VersionTLS13}, nil)
	_ = h.Tick(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if !bindsTo(t, got, nodes[0].Name) || bindsTo(t, got, nodes[0].Name+"-other") {
		t.Fatalf("the probe of %q is not bound to its leaf alone", nodes[0].Name)
	}
}
