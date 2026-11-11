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

// A preview released to the account but missing on a realtime node answers
// as off (ADR 20261021080640, amendment of 2026-11-09): apid must not issue
// tickets or accept access changes the nodes would not honor.
func TestPreviewGatesNeedEveryNode(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	s := &Service{PreviewReleased: func(*http.Request, state.Account, string, bool) bool { return true }}
	s.NodesServePreview = func(name string) bool { return name != realtime.NodePreviewTickets }
	if s.previewOn(r, state.Account{}, platformflags.RealtimeTickets, false) {
		t.Fatal("tickets released but not served by the nodes must be off")
	}
	if !s.previewOn(r, state.Account{}, platformflags.RealtimeAccess, false) {
		t.Fatal("access served by the nodes must follow the release")
	}
	s.PreviewReleased = func(*http.Request, state.Account, string, bool) bool { return false }
	if s.previewOn(r, state.Account{}, platformflags.RealtimeAccess, true) {
		t.Fatal("the nodes never release a preview the flag keeps off")
	}
}

func TestNodesServePreviewReadsFreshReports(t *testing.T) {
	o := &leasedRealtimeOwner{}
	if !o.NodesServePreview(realtime.NodePreviewAccess) {
		t.Fatal("no reports must leave the decision to the flags")
	}
	o.endpointSync.recordPreviews("old-node", nil)
	if !o.NodesServePreview(realtime.NodePreviewAccess) {
		t.Fatal("a node that predates the report must not count")
	}
	o.endpointSync.recordPreviews("node-a", []string{realtime.NodePreviewAccess, realtime.NodePreviewTickets})
	o.endpointSync.recordPreviews("node-b", []string{realtime.NodePreviewAccess})
	if !o.NodesServePreview(realtime.NodePreviewAccess) || o.NodesServePreview(realtime.NodePreviewTickets) {
		t.Fatal("a preview is served only when every reporting node lists it")
	}
	o.endpointSync.mu.Lock()
	o.endpointSync.previewsAt["node-b"] = o.endpointSync.previewsAt["node-b"].Add(-endpointInventoryFresh)
	o.endpointSync.mu.Unlock()
	if !o.NodesServePreview(realtime.NodePreviewTickets) {
		t.Fatal("a node silent past the inventory window must not count")
	}
}
