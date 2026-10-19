package managedrealtime

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// ticketTestHost serves one app from a MemStore and records audit kinds.
type ticketTestHost struct {
	store  *state.MemStore
	app    state.App
	audits []string
}

func (h *ticketTestHost) LoadApp(w http.ResponseWriter, _ *http.Request, _ state.Account, slug string) (state.App, bool) {
	if slug != h.app.Slug {
		http.NotFound(w, nil)
		return state.App{}, false
	}
	return h.app, true
}
func (h *ticketTestHost) Store() state.Store    { return h.store }
func (h *ticketTestHost) Logger() *slog.Logger  { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (h *ticketTestHost) Ops() *wire.OpsMetrics { return nil }
func (h *ticketTestHost) EmitAudit(_ context.Context, kind string, _ *string, _ map[string]any) {
	h.audits = append(h.audits, kind)
}
func (h *ticketTestHost) SecretRecipient() *age.X25519Recipient                         { return nil }
func (h *ticketTestHost) HostIdentitiesForUnseal(context.Context) []*age.X25519Identity { return nil }
func (h *ticketTestHost) ResolveAndCheckEgress(context.Context, string) *api.Problem    { return nil }

func TestRevokeRealtimeTicketHandlers(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "tickets-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "tickets", Status: state.AppActive, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.com/cb", Enabled: true,
	}, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	host := &ticketTestHost{store: store, app: app}
	svc := &Service{Host: host, TicketsPreviewEnabled: true}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/apps/{slug}/realtime/endpoints/{id}/tickets", func(w http.ResponseWriter, r *http.Request) { svc.IssueRealtimeTicket(w, r, acct) })
	mux.HandleFunc("DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets", func(w http.ResponseWriter, r *http.Request) { svc.RevokeRealtimeTickets(w, r, acct) })
	mux.HandleFunc("DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets/{ticket}", func(w http.ResponseWriter, r *http.Request) { svc.RevokeRealtimeTicket(w, r, acct) })
	base := "/v1/apps/tickets/realtime/endpoints/" + ep.ID + "/tickets"
	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	issue := func(principal string) api.RealtimeTicketResponse {
		t.Helper()
		rec := do(http.MethodPost, base, `{"principal":"`+principal+`","channels":["orders"],"ttl_seconds":60}`)
		var out api.RealtimeTicketResponse
		if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.TicketID == "" || out.Ticket == "" {
			t.Fatalf("issue = %d %s", rec.Code, rec.Body)
		}
		return out
	}

	first := issue("user-1")
	issue("user-1")
	issue("user-2")
	if rec := do(http.MethodDelete, base+"/"+first.TicketID, ""); rec.Code != http.StatusNoContent || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("revoke by id = %d %v", rec.Code, rec.Header())
	}
	if rec := do(http.MethodDelete, base+"/"+first.TicketID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("repeat revoke = %d, want 404", rec.Code)
	}
	if rec := do(http.MethodDelete, base+"/not-a-uuid", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id = %d, want 400", rec.Code)
	}
	rec := do(http.MethodDelete, base+"?principal=user-1", "")
	var revoked api.RealtimeTicketsRevokeResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &revoked) != nil || revoked.Revoked != 1 {
		t.Fatalf("revoke principal = %d %s, want 1 revoked", rec.Code, rec.Body)
	}
	if rec := do(http.MethodDelete, base, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("revoke without principal = %d, want 400", rec.Code)
	}
	if rec := do(http.MethodDelete, base+"?principal=a&principal=b", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("revoke with two principals = %d, want 400", rec.Code)
	}
	if rec := do(http.MethodDelete, "/v1/apps/tickets/realtime/endpoints/"+uuid.NewString()+"/tickets/"+first.TicketID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("revoke on an unknown endpoint = %d, want 404", rec.Code)
	}
	if strings.Join(host.audits, ",") != "realtime.ticket_issued,realtime.ticket_issued,realtime.ticket_issued,realtime.ticket_revoked,realtime.tickets_revoked" {
		t.Fatalf("audits = %v", host.audits)
	}

	svc.TicketsPreviewEnabled = false
	if rec := do(http.MethodDelete, base+"/"+first.TicketID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("revoke with the preview off = %d, want 404", rec.Code)
	}
}
