package managedrealtime

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

type removalRegistrar struct {
	liveRouteRegistrar
	removed []string
}

func (r *removalRegistrar) RemoveEndpoint(_ context.Context, id string) error {
	r.removed = append(r.removed, id)
	return nil
}

// adr: 20261023115741 — an endpoint whose account has realtime off is
// withdrawn from realtimed, unless live routes keep it serving live queries.
func TestEndpointWithdrawnWhileRealtimeIsOff(t *testing.T) {
	ctx := context.Background()
	row := state.ManagedRealtimeEndpoint{ID: "ep", AppID: "app-1", AccountID: "acct-1", Enabled: true, CallbackURL: "https://app.example"}
	route := realtime.LiveRoute{KID: "0123456789abcdef", Database: "db-1", URL: "wss://live.internal/db-1/live"}
	tests := []struct {
		name         string
		released     bool
		routes       []realtime.LiveRoute
		wantRegister bool
	}{
		{"realtime on", true, nil, true},
		{"realtime off", false, nil, false},
		{"realtime off with live routes", false, []realtime.LiveRoute{route}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registrar := &removalRegistrar{}
			var asked string
			s := &Service{
				Registrar: registrar,
				LiveRoutes: func(context.Context, string, string) ([]realtime.LiveRoute, error) {
					return tc.routes, nil
				},
				RealtimeReleased: func(_ context.Context, accountID, appID string) bool {
					asked = accountID + "/" + appID
					return tc.released
				},
			}
			if err := s.syncManagedRealtimeEndpoint(ctx, row); err != nil {
				t.Fatal(err)
			}
			registered := len(registrar.registered) == 1
			if registered != tc.wantRegister || registered == (len(registrar.removed) == 1) {
				t.Fatalf("registered %d, removed %v; want registered=%v", len(registrar.registered), registrar.removed, tc.wantRegister)
			}
			if tc.routes == nil && asked != "acct-1/app-1" {
				t.Fatalf("decided for %q, want acct-1/app-1", asked)
			}
		})
	}
}
