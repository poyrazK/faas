package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeContractDocStoreStub has no document under the preview's own ID but
// serves its production app's through the contract lookup, as the Postgres
// store does.
type routeContractDocStoreStub struct {
	contract []byte
}

func (routeContractDocStoreStub) GetAppOpenAPIDoc(context.Context, string, string) ([]byte, state.AppOpenAPIDocMeta, error) {
	return nil, state.AppOpenAPIDocMeta{}, state.ErrNotFound
}

func (s routeContractDocStoreStub) GetRouteContractOpenAPIDoc(context.Context, string, string) ([]byte, state.AppOpenAPIDocMeta, error) {
	return s.contract, state.AppOpenAPIDocMeta{}, nil
}

// A PR preview inherits its production app's route contract but has no
// imported OpenAPI document of its own. Read by its own ID, every request
// failed as declared_route_policy_unavailable; it is held to its production
// app's document instead.
func TestDeclaredRoutesMatcher_PreviewUsesProductionDocument(t *testing.T) {
	m := newDeclaredRoutesMatcher(routeContractDocStoreStub{
		contract: []byte(`{"openapi":"3.0.0","paths":{"/products":{"get":{}}}}`),
	})
	preview := gateway.App{ID: "pr-7-shop", AccountID: "acct-1", OnlyAllowDeclaredRoutes: true}
	for _, tc := range []struct {
		path, method string
		want         bool
	}{
		{path: "/products", method: "GET", want: true},
		{path: "/admin", method: "GET", want: false},
	} {
		got, err := m.MatchDeclaredRoute(context.Background(), preview, tc.path, tc.method)
		if err != nil {
			t.Fatalf("MatchDeclaredRoute(%s %s): %v", tc.method, tc.path, err)
		}
		if got != tc.want {
			t.Errorf("MatchDeclaredRoute(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
