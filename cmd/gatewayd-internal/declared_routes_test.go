package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type declaredRouteDocStoreStub struct {
	doc   []byte
	reads int
	err   error
}

func (s *declaredRouteDocStoreStub) GetAppOpenAPIDoc(context.Context, string, string) ([]byte, state.AppOpenAPIDocMeta, error) {
	s.reads++
	return s.doc, state.AppOpenAPIDocMeta{}, s.err
}

func TestDeclaredRoutesMatcher_OpenAPIAndCache(t *testing.T) {
	store := &declaredRouteDocStoreStub{doc: []byte(`{"openapi":"3.0.0","paths":{"/products":{"get":{}},"/orders/{id}":{"post":{}}}}`)}
	m := newDeclaredRoutesMatcher(store)
	app := gateway.App{ID: "app-1", AccountID: "acct-1", OnlyAllowDeclaredRoutes: true}

	for _, tc := range []struct {
		path, method string
		want         bool
	}{
		{path: "/products", method: "GET", want: true},
		{path: "/products/", method: "GET", want: true},
		{path: "/products", method: "POST", want: false},
		{path: "/orders/abc", method: "POST", want: true},
		{path: "/orders", method: "POST", want: false},
		{path: "/wp-login.php", method: "GET", want: false},
	} {
		got, err := m.MatchDeclaredRoute(context.Background(), app, tc.path, tc.method)
		if err != nil {
			t.Fatalf("MatchDeclaredRoute(%s %s): %v", tc.method, tc.path, err)
		}
		if got != tc.want {
			t.Errorf("MatchDeclaredRoute(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
	if store.reads != 1 {
		t.Fatalf("OpenAPI document reads = %d, want one cached read", store.reads)
	}
	m.Invalidate(app.ID)
	if _, err := m.MatchDeclaredRoute(context.Background(), app, "/products", "GET"); err != nil {
		t.Fatal(err)
	}
	if store.reads != 2 {
		t.Fatalf("OpenAPI document reads after invalidation = %d, want 2", store.reads)
	}
}

func TestDeclaredRoutesMatcher_ExplicitListTakesPrecedence(t *testing.T) {
	store := &declaredRouteDocStoreStub{doc: []byte(`{"openapi":"3.0.0","paths":{"/from-openapi":{"get":{}}}}`)}
	m := newDeclaredRoutesMatcher(store)
	app := gateway.App{
		ID: "app-2", AccountID: "acct-2", OnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: []gateway.DeclaredRoute{{Path: "/explicit/{id}", Methods: []string{"GET"}}},
	}
	got, err := m.MatchDeclaredRoute(context.Background(), app, "/explicit/42", "GET")
	if err != nil || !got {
		t.Fatalf("explicit route match = (%v, %v), want (true, nil)", got, err)
	}
	got, err = m.MatchDeclaredRoute(context.Background(), app, "/from-openapi", "GET")
	if err != nil || got {
		t.Fatalf("OpenAPI route should not be used when explicit list is set: (%v, %v)", got, err)
	}
	if store.reads != 0 {
		t.Fatalf("explicit route list caused %d OpenAPI reads", store.reads)
	}
}

func TestDeclaredRoutesMatcher_TTL(t *testing.T) {
	store := &declaredRouteDocStoreStub{doc: []byte(`{"openapi":"3.0.0","paths":{"/health":{"get":{}}}}`)}
	now := time.Unix(100, 0)
	m := newDeclaredRoutesMatcher(store)
	m.now = func() time.Time { return now }
	m.ttl = time.Second
	app := gateway.App{ID: "app-3", AccountID: "acct-3"}
	if _, err := m.MatchDeclaredRoute(context.Background(), app, "/health", "GET"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if _, err := m.MatchDeclaredRoute(context.Background(), app, "/health", "GET"); err != nil {
		t.Fatal(err)
	}
	if store.reads != 2 {
		t.Fatalf("reads after TTL expiry = %d, want 2", store.reads)
	}
}
