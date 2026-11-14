package edgejwks_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/edgejwks"
)

// adr: 091 — amendment 2026-11-04 (review round 12): the cache holds at most
// MaxCachedJWKSURLs URLs, so a jwks_url that keeps changing cannot grow a
// shared gateway without bound.
func TestCache_BoundsTheURLsItKeeps(t *testing.T) {
	body, _ := makeValidJWKS(t, "k1")
	srv := newServer(t, body, http.StatusOK, nil)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	url := func(i int) string { return fmt.Sprintf("%s/jwks?n=%d", srv.URL, i) }
	total := edgejwks.MaxCachedJWKSURLs + 20
	for i := range total {
		mustRegister(t, c, url(i))
		if _, ok, err := c.Get(context.Background(), url(i), "k1"); err != nil || !ok {
			t.Fatalf("Get %d: ok=%v err=%v", i, ok, err)
		}
	}
	for i := range 20 {
		if _, ok, _ := c.Get(context.Background(), url(i), "k1"); ok {
			t.Fatalf("URL %d is still cached past the bound", i)
		}
	}
	if _, ok, err := c.Get(context.Background(), url(total-1), "k1"); err != nil || !ok {
		t.Fatalf("the most recent URL was evicted: ok=%v err=%v", ok, err)
	}
	// An evicted URL registers and fetches again on its next token.
	mustRegister(t, c, url(0))
	if _, ok, err := c.Get(context.Background(), url(0), "k1"); err != nil || !ok {
		t.Fatalf("re-registered URL: ok=%v err=%v", ok, err)
	}
}

// adr: 091 — amendment 2026-11-04 (review round 12): the fetched documents
// of every URL together stay under MaxCachedJWKSBytes.
func TestCache_BoundsTheBytesItKeeps(t *testing.T) {
	body, _ := makeValidJWKS(t, "k1")
	// Pad the document inside its object to about 1.5 MiB, under the
	// per-document cap.
	padded := strings.TrimSuffix(body, "}") + strings.Repeat(" ", 3<<19) + "}"
	srv := newServer(t, padded, http.StatusOK, nil)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	url := func(i int) string { return fmt.Sprintf("%s/jwks?n=%d", srv.URL, i) }
	n := edgejwks.MaxCachedJWKSBytes/len(padded) + 10
	for i := range n {
		mustRegister(t, c, url(i))
		if _, ok, err := c.Get(context.Background(), url(i), "k1"); err != nil || !ok {
			t.Fatalf("Get %d: ok=%v err=%v", i, ok, err)
		}
	}
	kept := 0
	for i := range n {
		if _, ok, _ := c.Get(context.Background(), url(i), "k1"); ok {
			kept++
		}
	}
	if limit := edgejwks.MaxCachedJWKSBytes / len(padded); kept > limit {
		t.Fatalf("kept %d documents of %d bytes, want at most %d", kept, len(padded), limit)
	}
	if _, ok, _ := c.Get(context.Background(), url(n-1), "k1"); !ok {
		t.Fatal("the most recent URL was evicted")
	}
}
