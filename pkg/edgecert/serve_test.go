// adr: 20261103163954
package edgecert_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/edgecert"
)

// liveStore serves rows, or fails.
type liveStore struct {
	rows []edgecert.Stored
	err  error
}

func (s *liveStore) Live(context.Context, string) ([]edgecert.Stored, error) { return s.rows, s.err }

// stored issues a certificate for names through the fake CA, sealed to id.
func stored(t *testing.T, ca *fakeCA, names []string, id *age.X25519Identity, rowID int64) edgecert.Stored {
	t.Helper()
	c, err := edgecert.Issue(t.Context(), ca, names, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	return edgecert.Stored{ID: rowID, Names: c.Names, ChainPEM: c.ChainPEM, KeySealed: c.KeySealed, NotBefore: c.NotBefore, NotAfter: c.NotAfter}
}

func newServed(t *testing.T, store edgecert.LiveStore, ids ...*age.X25519Identity) *edgecert.Served {
	t.Helper()
	s, err := edgecert.NewServed(edgecert.CALetsEncrypt, ids, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func serverName(s *edgecert.Served, name string) (*tls.Certificate, error) {
	return s.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
}

// The edge opens the key in memory and serves the wildcard to one label
// below it, and to nothing else.
func TestServedServesTheWildcardItOpened(t *testing.T) {
	ca, id := newFakeCA(t), newIdentity(t)
	store := &liveStore{rows: []edgecert.Stored{stored(t, ca, wildcard, id, 1)}}
	s := newServed(t, store, id)
	if s.Loaded() {
		t.Fatal("loaded before Load")
	}
	if changed, err := s.Load(t.Context()); err != nil || !changed || !s.Loaded() {
		t.Fatalf("Load = %v, %v", changed, err)
	}
	for _, name := range []string{"app.gregale.dev", "APP.gregale.dev."} {
		cert, err := serverName(s, name)
		if err != nil || cert.Leaf == nil || cert.Leaf.DNSNames[0] != "*.gregale.dev" || len(cert.Certificate) != 2 {
			t.Fatalf("%s: %v, %v", name, cert, err)
		}
		if err := cert.Leaf.VerifyHostname(strings.TrimSuffix(strings.ToLower(name), ".")); err != nil {
			t.Fatalf("%s: the served leaf does not cover it: %v", name, err)
		}
	}
	for _, name := range []string{"gregale.dev", "a.b.gregale.dev", "app.other.dev", ""} {
		if _, err := serverName(s, name); err == nil {
			t.Errorf("%q got a certificate", name)
		}
	}
	if changed, err := s.Load(t.Context()); err != nil || changed {
		t.Fatalf("reloading the same row: changed %v, %v", changed, err)
	}
}

// An outage, an empty result or a key the edge cannot open never takes the
// served certificate away; a newer certificate replaces it.
func TestServedKeepsItsCertificateUntilABetterOneLoads(t *testing.T) {
	ca, id := newFakeCA(t), newIdentity(t)
	first := stored(t, ca, wildcard, id, 1)
	store := &liveStore{rows: []edgecert.Stored{first}}
	s := newServed(t, store, id)
	if _, err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	served := func() int64 {
		cert, err := serverName(s, "app.gregale.dev")
		if err != nil {
			t.Fatal(err)
		}
		return cert.Leaf.SerialNumber.Int64()
	}
	want := served()
	for _, tc := range []struct {
		name string
		edit func()
	}{
		{"postgres down", func() { store.err = errors.New("connection refused") }},
		{"no live row", func() { store.err, store.rows = nil, nil }},
		{"sealed to another identity", func() { store.rows = []edgecert.Stored{stored(t, ca, wildcard, newIdentity(t), 2)} }},
		{"corrupt key", func() {
			bad := first
			bad.ID, bad.KeySealed = 3, []byte("not age")
			store.rows = []edgecert.Stored{bad}
		}},
	} {
		tc.edit()
		if _, err := s.Load(t.Context()); err == nil {
			t.Errorf("%s: Load reported no error", tc.name)
		}
		if got := served(); got != want || !s.Loaded() {
			t.Fatalf("%s: serving serial %x, want %x", tc.name, got, want)
		}
	}
	store.err, store.rows = nil, []edgecert.Stored{stored(t, ca, wildcard, id, 4)}
	if changed, err := s.Load(t.Context()); err != nil || !changed || served() == want {
		t.Fatalf("a newer certificate: changed %v, %v", changed, err)
	}
}

// Of two certificates covering a name, the one that lasts longer is served.
func TestServedPrefersTheLongerLastingCertificate(t *testing.T) {
	ca, id := newFakeCA(t), newIdentity(t)
	short := stored(t, ca, []string{"app.gregale.dev"}, id, 1)
	ca.now = ca.now.Add(time.Hour)
	long := stored(t, ca, wildcard, id, 2)
	s := newServed(t, &liveStore{rows: []edgecert.Stored{short, long}}, id)
	if _, err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if cert, err := serverName(s, "app.gregale.dev"); err != nil || cert.Leaf.DNSNames[0] != "*.gregale.dev" {
		t.Fatalf("served %v, %v; want the wildcard, which expires later", cert, err)
	}
}

func TestServedReportsTimeLeftPerName(t *testing.T) {
	ca, id := newFakeCA(t), newIdentity(t)
	row := stored(t, ca, wildcard, id, 1)
	s := newServed(t, &liveStore{rows: []edgecert.Stored{row}}, id)
	reg := prometheus.NewRegistry()
	reg.MustRegister(s.Collectors()...)
	if n, err := testutil.GatherAndCount(reg, "edge_certificate_expiry_seconds"); err != nil || n != 0 {
		t.Fatalf("expiry series before a load: %d, %v", n, err)
	}
	if _, err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "edge_certificate_expiry_seconds" {
			continue
		}
		m := f.GetMetric()
		left := time.Until(row.NotAfter).Seconds()
		if len(m) != 1 || m[0].GetLabel()[0].GetValue() != "*.gregale.dev" || m[0].GetGauge().GetValue() > left+1 || m[0].GetGauge().GetValue() < left-60 {
			t.Fatalf("expiry = %v, want about %.0f s for *.gregale.dev", m, left)
		}
		return
	}
	t.Fatal("no edge_certificate_expiry_seconds after a load")
}

func TestNewServedNeedsAnIdentityAndAKnownCA(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := edgecert.NewServed(edgecert.CALetsEncrypt, nil, &liveStore{}, log); err == nil {
		t.Fatal("no identity accepted")
	}
	if _, err := edgecert.NewServed("zerossl", []*age.X25519Identity{newIdentity(t)}, &liveStore{}, log); err == nil {
		t.Fatal("an unknown CA accepted")
	}
}
