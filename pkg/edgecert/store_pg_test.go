package edgecert_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/edgecert"
)

// The newest certificate is the one issued last, not the one that expires
// last (ADR-20261108100000). When short-lived certificates replace a 90-day
// one, the 90-day one still expires later; ordering by expiry would keep
// it newest, so edgednsd would order again every hour and every edge would
// keep serving the old key.
func TestPgStoreNewestIsTheLastIssued(t *testing.T) {
	pool := pgtest.Open(t)
	schema, err := os.ReadFile("certpg/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), string(schema)); err != nil {
		t.Fatal(err)
	}
	store := edgecert.NewPgStore(pool)
	now := time.Now().UTC().Truncate(time.Second)
	row := func(i int, notBefore, notAfter time.Time) edgecert.Certificate {
		return edgecert.Certificate{
			Names: wildcard, CA: edgecert.CALetsEncrypt, AccountURI: "https://acme.example/acct/1",
			Serial: fmt.Sprintf("%02x", i), SHA256: strings.Repeat(fmt.Sprintf("%x", i), 64),
			ChainPEM: "chain", KeySealed: []byte("sealed"), KeyKID: "kid",
			NotBefore: notBefore, NotAfter: notAfter,
		}
	}
	longLived := row(1, now.Add(-30*24*time.Hour), now.Add(60*24*time.Hour))
	shortLived := row(2, now.Add(-time.Hour), now.Add(159*time.Hour))
	for _, c := range []edgecert.Certificate{longLived, shortLived} {
		if _, err := store.Insert(t.Context(), c); err != nil {
			t.Fatal(err)
		}
	}
	terms, found, err := store.NewestTerms(t.Context(), wildcard, edgecert.CALetsEncrypt)
	if err != nil || !found || terms.SHA256 != shortLived.SHA256 {
		t.Fatalf("NewestTerms = %+v %v %v, want the short-lived certificate", terms, found, err)
	}
	live, err := store.Live(t.Context(), edgecert.CALetsEncrypt)
	if err != nil || len(live) != 2 || !live[0].NotAfter.Equal(shortLived.NotAfter) {
		t.Fatalf("Live = %+v %v, want the short-lived certificate first", live, err)
	}
}
