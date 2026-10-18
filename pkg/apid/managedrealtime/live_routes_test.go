package managedrealtime

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type liveRouteRegistrar struct{ registered []realtime.Endpoint }

func (r *liveRouteRegistrar) RegisterEndpoint(_ context.Context, e realtime.Endpoint) error {
	r.registered = append(r.registered, e)
	return nil
}

func (r *liveRouteRegistrar) RemoveEndpoint(context.Context, string) error { return nil }

// adr: 20261016151947 — an endpoint is registered with the live routes of databases
// bound to its app; a failed lookup leaves the node's registration alone.
func TestEndpointRegistrationCarriesLiveRoutes(t *testing.T) {
	ctx := context.Background()
	route := realtime.LiveRoute{KID: "0123456789abcdef", Database: "db-1", URL: "wss://live.internal/db-1/live"}
	registrar := &liveRouteRegistrar{}
	var asked string
	s := &Service{Registrar: registrar, LiveRoutes: func(_ context.Context, accountID, appID string) ([]realtime.LiveRoute, error) {
		asked = accountID + "/" + appID
		return []realtime.LiveRoute{route}, nil
	}}
	row := state.ManagedRealtimeEndpoint{ID: "ep", AppID: "app-1", AccountID: "acct-1", Enabled: true, CallbackURL: "https://app.example"}
	if err := s.syncManagedRealtimeEndpoint(ctx, row); err != nil {
		t.Fatal(err)
	}
	if asked != "acct-1/app-1" || len(registrar.registered) != 1 {
		t.Fatalf("asked %q, registered %d", asked, len(registrar.registered))
	}
	if got := registrar.registered[0].LiveRoutes; len(got) != 1 || got[0] != route {
		t.Fatalf("live routes = %v", got)
	}

	s.LiveRoutes = func(context.Context, string, string) ([]realtime.LiveRoute, error) { return nil, errors.New("database down") }
	if err := s.syncManagedRealtimeEndpoint(ctx, row); err == nil {
		t.Fatal("a failed route lookup registered the endpoint")
	}
	if len(registrar.registered) != 1 {
		t.Fatal("a failed route lookup replaced the registration")
	}
}
