// adr: 20261108124106
package instancestats_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
	"github.com/onebox-faas/faas/pkg/state"
)

// The stats poll dials each node bound to that node's name.
func TestPollerBindsEachDialToTheNodeName(t *testing.T) {
	store := state.NewMemStore()
	nodes, err := store.ActiveComputeNodes(context.Background())
	if err != nil || len(nodes) != 1 {
		t.Fatalf("seeded nodes = %v, %v; want one", nodes, err)
	}
	var (
		mu  sync.Mutex
		got *tls.Config
	)
	dialer := instancestats.DialerFunc(func(_ context.Context, _ string, cfg *tls.Config) (sched.VMM, error) {
		mu.Lock()
		got = cfg
		mu.Unlock()
		return nil, errors.New("test: dial refused")
	})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := instancestats.NewPoller(store, dialer, &tls.Config{MinVersion: tls.VersionTLS13}, instancestats.NewReader(), nil, log)
	_ = p.Tick(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if got == nil || got.VerifyPeerCertificate == nil {
		t.Fatal("the stats dial has no node binding")
	}
	leaf := func(cn string) [][]*x509.Certificate {
		return [][]*x509.Certificate{{{Subject: pkix.Name{CommonName: cn}}}}
	}
	if err := got.VerifyPeerCertificate(nil, leaf(nodes[0].Name)); err != nil {
		t.Fatalf("the node's own leaf was refused: %v", err)
	}
	if got.VerifyPeerCertificate(nil, leaf(nodes[0].Name+"-other")) == nil {
		t.Fatal("another node's leaf was accepted")
	}
}
