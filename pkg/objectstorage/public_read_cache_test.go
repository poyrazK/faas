package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// countingPublicReadStore counts the control-plane reads the public-read
// matcher makes. fail, when set, makes the next AppBySlug return it once.
type countingPublicReadStore struct {
	Store       PublicReadStore
	appBySlug   int
	listBuckets int
	fail        error
}

func (s *countingPublicReadStore) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	s.appBySlug++
	if err := s.fail; err != nil {
		s.fail = nil
		return state.App{}, err
	}
	return s.Store.AppBySlug(ctx, slug)
}

func (s *countingPublicReadStore) AppByID(ctx context.Context, id string) (state.App, error) {
	return s.Store.AppByID(ctx, id)
}

func (s *countingPublicReadStore) DomainByName(ctx context.Context, domain string) (state.CustomDomain, error) {
	return s.Store.DomainByName(ctx, domain)
}

func (s *countingPublicReadStore) ListObjectBuckets(ctx context.Context, accountID, appID string) ([]state.ObjectBucket, error) {
	s.listBuckets++
	return s.Store.ListObjectBuckets(ctx, accountID, appID)
}

func publicReadRoute(t *testing.T, h http.Handler, host, path string) string {
	t.Helper()
	var hit string
	f := h.(*publicReadForwarder)
	f.forward = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = "forward" })
	f.next = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = "next" })
	req := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
	req.Host = host
	h.ServeHTTP(httptest.NewRecorder(), req)
	return hit
}

// Every app request passes through the matcher before the gateway's rate
// limits; a host's mounts must not cost control-plane reads per request.
func TestPublicReadLookupReusesHostMounts(t *testing.T) {
	store := &countingPublicReadStore{Store: seedForwardBucket(t)}
	h, err := NewPublicReadForwarder(PublicReadForwardConfig{
		Store: store, AppsDomain: "gregale.dev", Forward: http.NotFoundHandler(), Next: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets/logo.png", "/api/users", "/assets/app.js"} {
		want := "forward"
		if path == "/api/users" {
			want = "next"
		}
		if got := publicReadRoute(t, h, "assets-app.gregale.dev", path); got != want {
			t.Fatalf("%s went to %q, want %q", path, got, want)
		}
	}
	if store.appBySlug != 1 || store.listBuckets != 1 {
		t.Fatalf("three requests to one host read the store %d+%d times, want 1+1", store.appBySlug, store.listBuckets)
	}

	for range 3 {
		if got := publicReadRoute(t, h, "no-such-app.gregale.dev", "/assets/logo.png"); got != "next" {
			t.Fatalf("unknown host went to %q, want next", got)
		}
	}
	if store.appBySlug != 2 || store.listBuckets != 1 {
		t.Fatalf("an unknown host was re-resolved: %d+%d reads, want 2+1", store.appBySlug, store.listBuckets)
	}
}

func TestPublicReadLookupDoesNotCacheErrors(t *testing.T) {
	store := &countingPublicReadStore{Store: seedForwardBucket(t), fail: errors.New("postgres unavailable")}
	h, err := NewPublicReadForwarder(PublicReadForwardConfig{
		Store: store, AppsDomain: "gregale.dev", Forward: http.NotFoundHandler(), Next: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "next" {
		t.Fatalf("failed lookup went to %q, want next", got)
	}
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "forward" {
		t.Fatalf("lookup after a transient failure went to %q, want forward", got)
	}
}

func TestPublicMountCacheExpiresAndStaysBounded(t *testing.T) {
	c := newPublicMountCache()
	now := time.Now()
	c.put("a.example", []state.ObjectBucket{{ID: "b"}}, now)
	if got, ok := c.get("a.example", now.Add(publicMountTTL-time.Millisecond)); !ok || len(got) != 1 {
		t.Fatal("fresh entry missed")
	}
	if _, ok := c.get("a.example", now.Add(publicMountTTL)); ok {
		t.Fatal("expired entry still served")
	}
	for i := range publicMountMaxHosts + 10 {
		c.put(fmt.Sprintf("h%d.example", i), nil, now)
	}
	if n := len(c.hosts); n > publicMountMaxHosts {
		t.Fatalf("cache holds %d hosts, cap %d", n, publicMountMaxHosts)
	}
}
