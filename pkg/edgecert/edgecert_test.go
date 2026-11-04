// adr: 20261103163954
package edgecert_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgecert"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

var wildcard = []string{"*.gregale.dev"}

// fakeCA signs whatever key it is given for the names it is asked, unless
// told to fail or to sign another key or other names.
type fakeCA struct {
	t       *testing.T
	key     *ecdsa.PrivateKey
	cert    *x509.Certificate
	orders  int
	fail    error
	swapKey bool
	names   []string
	now     time.Time
}

func newFakeCA(t *testing.T) *fakeCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake CA"}, IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &fakeCA{t: t, key: key, cert: cert, now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeCA) CA() string { return edgecert.CALetsEncryptStaging }

func (f *fakeCA) Order(_ context.Context, key crypto.Signer, names []string) ([]byte, string, error) {
	f.orders++
	if f.fail != nil {
		return nil, "", f.fail
	}
	pub := key.Public()
	if f.swapKey {
		other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		pub = &other.PublicKey
	}
	if f.names != nil {
		names = f.names
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(int64(0xabc000 + f.orders)), DNSNames: names,
		NotBefore: f.now, NotAfter: f.now.Add(90 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, f.cert, pub, f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.cert.Raw})...)
	return chain, "https://acme-staging-v02.api.letsencrypt.org/acme/acct/1", nil
}

func newIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The stored key opens only with the edge-tls identity, under the
// certificate-key namespace, and is the key the leaf carries.
func TestIssueSealsTheLeafsOwnKey(t *testing.T) {
	ca := newFakeCA(t)
	id := newIdentity(t)
	cert, err := edgecert.Issue(t.Context(), ca, wildcard, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if cert.CA != edgecert.CALetsEncryptStaging || cert.KeyKID != id.Recipient().String() || cert.Serial != "abc001" || len(cert.SHA256) != 64 || !cert.NotAfter.After(cert.NotBefore) {
		t.Fatalf("certificate = %+v", cert)
	}
	if _, _, err := secretbox.OpenBytes(newIdentity(t), cert.KeySealed); err == nil {
		t.Fatal("another identity opened the key")
	}
	ns, der, err := secretbox.OpenBytes(id, cert.KeySealed)
	if err != nil || ns != edgecert.KeyNamespace {
		t.Fatalf("open = %q, %v", ns, err)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(cert.ChainPEM))
	leaf, _ := x509.ParseCertificate(block.Bytes)
	if ec, ok := key.(*ecdsa.PrivateKey); !ok || ec.Curve != elliptic.P256() || !ec.PublicKey.Equal(leaf.PublicKey) {
		t.Fatalf("sealed key %T does not match the leaf", key)
	}
}

// A certificate for another key or other names is never stored.
func TestIssueRefusesALeafItDidNotAskFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*fakeCA)
		want string
	}{
		{"another key", func(f *fakeCA) { f.swapKey = true }, "does not carry the key"},
		{"other names", func(f *fakeCA) { f.names = []string{"*.evil.dev"} }, "the leaf names"},
		{"order fails", func(f *fakeCA) { f.fail = errors.New("rate limited") }, "rate limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ca := newFakeCA(t)
			tc.edit(ca)
			if _, err := edgecert.Issue(t.Context(), ca, wildcard, newIdentity(t).Recipient()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Issue err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestNormalizeNames(t *testing.T) {
	got, err := edgecert.NormalizeNames([]string{" *.Gregale.dev", "gregale-preview.dev"})
	if err != nil || strings.Join(got, ",") != "*.gregale.dev,gregale-preview.dev" {
		t.Fatalf("NormalizeNames = %v, %v", got, err)
	}
	for _, bad := range [][]string{nil, {"*.*.gregale.dev"}, {"gregale"}, {"-x.dev"}, {"a_b.dev"}, {"*.gregale.dev", "*.GREGALE.dev"}, make([]string, api.EdgeCertificateNamesMax+1)} {
		if _, err := edgecert.NormalizeNames(bad); err == nil {
			t.Errorf("NormalizeNames(%q) accepted", bad)
		}
	}
}

func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	fresh := &edgecert.Terms{KeyKID: "k1", NotAfter: now.Add(api.EdgeCertificateRenewBefore + time.Minute)}
	for _, tc := range []struct {
		name   string
		newest *edgecert.Terms
		kid    string
		due    bool
	}{
		{"none", nil, "k1", true},
		{"fresh", fresh, "k1", false},
		{"recipient changed", fresh, "k2", true},
		{"inside the renewal window", &edgecert.Terms{KeyKID: "k1", NotAfter: now.Add(api.EdgeCertificateRenewBefore)}, "k1", true},
	} {
		if due, reason := edgecert.Due(tc.newest, tc.kid, now); due != tc.due || due == (reason == "") {
			t.Errorf("%s: Due = %v %q, want %v", tc.name, due, reason, tc.due)
		}
	}
}

// fakeStore keeps certificates in memory and fails inserts on request.
type fakeStore struct {
	certs      []edgecert.Certificate
	failInsert int
	pruned     []time.Time
}

func (s *fakeStore) NewestTerms(_ context.Context, names []string, ca string) (edgecert.Terms, bool, error) {
	var newest *edgecert.Certificate
	for i, c := range s.certs {
		if strings.Join(c.Names, ",") == strings.Join(names, ",") && c.CA == ca && (newest == nil || !c.NotAfter.Before(newest.NotAfter)) {
			newest = &s.certs[i]
		}
	}
	if newest == nil {
		return edgecert.Terms{}, false, nil
	}
	return edgecert.Terms{AccountURI: newest.AccountURI, KeyKID: newest.KeyKID, SHA256: newest.SHA256, NotAfter: newest.NotAfter}, true, nil
}

func (s *fakeStore) Insert(_ context.Context, c edgecert.Certificate) (int64, error) {
	if s.failInsert > 0 {
		s.failInsert--
		return 0, errors.New("database down")
	}
	s.certs = append(s.certs, c)
	return int64(len(s.certs)), nil
}

func (s *fakeStore) DeleteExpiredBefore(_ context.Context, t time.Time) (int64, error) {
	s.pruned = append(s.pruned, t)
	return 0, nil
}

func newManager(ca *fakeCA, store *fakeStore, id *age.X25519Identity, now *time.Time) *edgecert.Manager {
	return &edgecert.Manager{Names: wildcard, Recipient: id.Recipient(), Orderer: ca, Store: store,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return *now }}
}

// The first check orders; later checks leave a fresh certificate alone and
// renew it inside the renewal window.
func TestManagerOrdersOnceAndRenewsBeforeExpiry(t *testing.T) {
	ca, store, id := newFakeCA(t), &fakeStore{}, newIdentity(t)
	now := ca.now
	m := newManager(ca, store, id, &now)
	if wait := m.Step(t.Context()); wait != api.EdgeCertificateCheckInterval || ca.orders != 1 || len(store.certs) != 1 {
		t.Fatalf("first step: wait %v, orders %d, stored %d", wait, ca.orders, len(store.certs))
	}
	now = now.Add(24 * time.Hour)
	m.Step(t.Context())
	if ca.orders != 1 {
		t.Fatalf("a fresh certificate was ordered again (%d orders)", ca.orders)
	}
	now = store.certs[0].NotAfter.Add(-api.EdgeCertificateRenewBefore)
	ca.now = now
	m.Step(t.Context())
	if ca.orders != 2 || len(store.certs) != 2 {
		t.Fatalf("renewal: orders %d, stored %d", ca.orders, len(store.certs))
	}
	if len(store.pruned) == 0 || !store.pruned[len(store.pruned)-1].Equal(now.Add(-api.EdgeCertificateKeepAfterExpiry)) {
		t.Fatalf("pruned before %v, want %v", store.pruned, now.Add(-api.EdgeCertificateKeepAfterExpiry))
	}
}

// A failed order waits, doubling up to the cap, and is not retried early.
func TestManagerBacksOffAfterAFailedOrder(t *testing.T) {
	ca, store, id := newFakeCA(t), &fakeStore{}, newIdentity(t)
	ca.fail = errors.New("too many failed authorizations")
	now := ca.now
	m := newManager(ca, store, id, &now)
	var waits []time.Duration
	for range 6 {
		wait := m.Step(t.Context())
		waits = append(waits, wait)
		if m.Step(t.Context()); ca.orders != len(waits) {
			t.Fatalf("ordered %d times after %d waits: retried before the pause ended", ca.orders, len(waits))
		}
		now = now.Add(wait)
	}
	want := []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, api.EdgeCertificateRetryMax, api.EdgeCertificateRetryMax}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits = %v, want %v", waits, want)
		}
	}
	ca.fail = nil
	if m.Step(t.Context()); len(store.certs) != 1 {
		t.Fatal("no certificate after the CA recovered")
	}
}

// An issued certificate the store refused is stored on the next check, not
// ordered again.
func TestManagerStoresAnIssuedCertificateBeforeOrderingAgain(t *testing.T) {
	ca, store, id := newFakeCA(t), &fakeStore{failInsert: 2}, newIdentity(t)
	now := ca.now
	m := newManager(ca, store, id, &now)
	for range 3 {
		m.Step(t.Context())
		now = now.Add(api.EdgeCertificateRetryMin)
	}
	if ca.orders != 1 || len(store.certs) != 1 {
		t.Fatalf("orders %d, stored %d; want one order stored once", ca.orders, len(store.certs))
	}
}

// A new edge-tls recipient means a new certificate: edgednsd cannot open
// the stored key to re-seal it.
func TestManagerReordersForANewRecipient(t *testing.T) {
	ca, store := newFakeCA(t), &fakeStore{}
	now := ca.now
	newManager(ca, store, newIdentity(t), &now).Step(t.Context())
	next := newIdentity(t)
	newManager(ca, store, next, &now).Step(t.Context())
	if ca.orders != 2 || store.certs[1].KeyKID != next.Recipient().String() {
		t.Fatalf("orders %d, newest kid %q", ca.orders, store.certs[len(store.certs)-1].KeyKID)
	}
}
