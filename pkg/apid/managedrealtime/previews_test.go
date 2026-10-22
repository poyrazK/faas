package managedrealtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021080640 — apid's realtime preview decisions travel with the
// endpoint registration, and its API gates follow the platform flags.

func TestEndpointRegistrationCarriesPreviewDecisions(t *testing.T) {
	ctx := context.Background()
	registrar := &liveRouteRegistrar{}
	s := &Service{Registrar: registrar}
	row := state.ManagedRealtimeEndpoint{ID: "ep", AppID: "app-1", AccountID: "acct-1", Enabled: true, CallbackURL: "https://app.example"}
	if err := s.syncManagedRealtimeEndpoint(ctx, row); err != nil {
		t.Fatal(err)
	}
	if registrar.registered[0].Previews != nil {
		t.Fatal("without a decider the registration must carry no previews")
	}

	s.EndpointPreviews = func(_ context.Context, accountID, appID string) (*realtime.EndpointPreviews, error) {
		if accountID != "acct-1" || appID != "app-1" {
			t.Errorf("asked for %s/%s", accountID, appID)
		}
		return &realtime.EndpointPreviews{Tickets: true}, nil
	}
	if err := s.syncManagedRealtimeEndpoint(ctx, row); err != nil {
		t.Fatal(err)
	}
	if p := registrar.registered[1].Previews; p == nil || !p.Tickets || p.Access {
		t.Fatalf("previews = %+v", p)
	}

	s.EndpointPreviews = func(context.Context, string, string) (*realtime.EndpointPreviews, error) {
		return nil, errors.New("store down")
	}
	if err := s.syncManagedRealtimeEndpoint(ctx, row); err == nil || len(registrar.registered) != 2 {
		t.Fatal("a failed decision must leave the node's registration as it was")
	}
}

func TestPreviewGatesFollowTheReleaseDecision(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	s := &Service{TicketsPreviewEnabled: true}
	if !s.previewOn(r, state.Account{}, platformflags.RealtimeTickets, s.TicketsPreviewEnabled) {
		t.Fatal("without a decider the environment gate decides")
	}
	s.PreviewReleased = func(_ *http.Request, _ state.Account, key string, legacy bool) bool {
		return key != platformflags.RealtimeTickets && legacy
	}
	if s.previewOn(r, state.Account{}, platformflags.RealtimeTickets, s.TicketsPreviewEnabled) {
		t.Fatal("the platform flag must override the environment gate")
	}
}
