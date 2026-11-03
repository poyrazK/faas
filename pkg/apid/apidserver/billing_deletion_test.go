package apidserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

// deletionPolar is a Polar API that reports one subscription's status and
// records cancellations.
type deletionPolar struct {
	mu        sync.Mutex
	status    string // "" answers 404
	failPatch bool
	cancelled []string
}

func (p *deletionPolar) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		id, ok := strings.CutPrefix(r.URL.Path, "/v1/subscriptions/")
		if !ok {
			t.Error("unexpected provider request", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if p.status == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"id":"`+id+`","status":"`+p.status+`","product_id":"pro-product"}`)
		case http.MethodPatch:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["cancel_at_period_end"] != true {
				t.Errorf("cancel body = %v (%v)", body, err)
			}
			if p.failPatch {
				http.Error(w, `{"detail":"rejected"}`, http.StatusBadRequest)
				return
			}
			p.cancelled = append(p.cancelled, id)
			_, _ = io.WriteString(w, `{"id":"`+id+`","current_period_end":"2026-11-30T00:00:00Z","cancel_at_period_end":true}`)
		default:
			t.Error("unexpected provider method", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (p *deletionPolar) cancellations() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.cancelled...)
}

func deletionProvider(t *testing.T, fake *deletionPolar) *polar.Provider {
	t.Helper()
	provider, err := polar.NewProvider(polar.Config{APIKey: "private-offline-deletion-test-token", WebhookSecret: freshnessWebhookSecret,
		BaseURL: fake.serve(t).URL, HobbyProductID: "hobby-product", ProProductID: "pro-product", ScaleProductID: "scale-product"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// Hunt 17 H17-2: deleting an account cancels its subscription at period end,
// so a deleted customer is never charged again, and a subscription that
// already ended needs no cancellation. The cancellation runs after the
// deletion is recorded (bug hunt 2026-10-27 B3): a provider failure leaves the
// deletion scheduled and answers 503, and a repeat request finishes it.
func TestAccountDeletionEndsTheBillingSubscription(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        string
		failPatch     bool
		wantCode      int
		wantCancelled bool
	}{
		{name: "live subscription", status: "active", wantCode: http.StatusOK, wantCancelled: true},
		{name: "overdue subscription", status: "past_due", wantCode: http.StatusOK, wantCancelled: true},
		{name: "already ended", status: "canceled", wantCode: http.StatusOK},
		{name: "gone at the provider", status: "", wantCode: http.StatusOK},
		{name: "provider refuses", status: "active", failPatch: true, wantCode: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &deletionPolar{status: tc.status, failPatch: tc.failPatch}
			e := apidserver.Setup(t, api.PlanPro)
			(*e.S()).WithBillingProvider(deletionProvider(t, fake))
			store := *e.Store()
			if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: e.Acct().ID, Provider: "polar", CustomerID: "polar-del-" + e.Acct().ID, SubscriptionID: "sub-del"}); err != nil {
				t.Fatal(err)
			}
			rec := e.Do(t, http.MethodDelete, "/v1/account", nil, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("DELETE /v1/account = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantCode)
			}
			acct, err := store.AccountByID(t.Context(), e.Acct().ID)
			if err != nil {
				t.Fatal(err)
			}
			if acct.Status != state.AccountDeletedPending {
				t.Fatalf("account status = %s, want %s", acct.Status, state.AccountDeletedPending)
			}
			if got := fake.cancellations(); tc.wantCancelled != (len(got) == 1 && got[0] == "sub-del") {
				t.Fatalf("cancellations = %v, want cancelled=%v", got, tc.wantCancelled)
			}
			if !tc.failPatch {
				return
			}
			fake.mu.Lock()
			fake.failPatch = false
			fake.mu.Unlock()
			if rec := e.Do(t, http.MethodDelete, "/v1/account", nil, nil); rec.Code != http.StatusOK {
				t.Fatalf("repeat DELETE /v1/account = %d %s, want 200", rec.Code, rec.Body.String())
			}
			if got := fake.cancellations(); len(got) != 1 || got[0] != "sub-del" {
				t.Fatalf("cancellations after the repeat = %v, want the subscription", got)
			}
		})
	}
}

// The hard delete removes the billing identity with the account, so it is the
// last chance to stop a renewal: a non-payment deletion never cancelled, and a
// deletion scheduled before this fix. A refusal keeps the account for the next
// grace tick.
func TestHardDeleteEndsTheBillingSubscriptionFirst(t *testing.T) {
	for _, failPatch := range []bool{false, true} {
		fake := &deletionPolar{status: "active", failPatch: failPatch}
		e := apidserver.Setup(t, api.PlanPro)
		s := *e.S()
		s.WithBillingProvider(deletionProvider(t, fake))
		store := *e.Store()
		if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: e.Acct().ID, Provider: "polar", CustomerID: "polar-hard-" + e.Acct().ID, SubscriptionID: "sub-hard"}); err != nil {
			t.Fatal(err)
		}
		// Scheduled straight in the store, as a deletion from before this
		// change was: nothing cancelled the subscription then.
		if err := store.MarkAccountDeletionPending(t.Context(), e.Acct().ID); err != nil {
			t.Fatal(err)
		}
		acct, err := store.AccountByID(t.Context(), e.Acct().ID)
		if err != nil {
			t.Fatal(err)
		}
		// The cancellation runs before any other erasure step; the bucket
		// step then still waits for the grace window to lapse.
		err = s.BeforeAccountDelete(t.Context(), acct)
		if failPatch {
			if err == nil || !strings.Contains(err.Error(), "cancel billing subscription") {
				t.Fatalf("BeforeAccountDelete = %v; a refused cancellation must keep the account for the next grace tick", err)
			}
			continue
		}
		if err != nil && strings.Contains(err.Error(), "billing") {
			t.Fatalf("BeforeAccountDelete: %v", err)
		}
		if got := fake.cancellations(); len(got) != 1 || got[0] != "sub-hard" {
			t.Fatalf("cancellations = %v, want the account's subscription", got)
		}
	}
}

// Organizations are never hard-deleted, so deleting one must cancel its own
// subscription. The cancellation runs once the organization is closed; a
// provider failure answers 503 and a repeat DELETE finishes it.
func TestOrganizationDeletionEndsItsBillingSubscription(t *testing.T) {
	for _, failPatch := range []bool{false, true} {
		fake := &deletionPolar{status: "active", failPatch: failPatch}
		e := apidserver.Setup(t, api.PlanPro)
		(*e.S()).WithBillingProvider(deletionProvider(t, fake))
		org := apidserver.SeedSharedOrgWithOwner(t, e, "acme-h17", "Acme H17", api.PlanPro)
		store := *e.Store()
		if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: org.ID, Provider: "polar", CustomerID: "polar-org-" + org.ID, SubscriptionID: "sub-org"}); err != nil {
			t.Fatal(err)
		}
		rec := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug, nil, map[string]string{"X-Active-Org": org.Slug})
		after, err := store.OrgByID(t.Context(), org.ID)
		if err != nil {
			t.Fatal(err)
		}
		if failPatch {
			if rec.Code != http.StatusServiceUnavailable || !after.DeletedPending {
				t.Fatalf("refused cancellation: DELETE = %d %s, deleted_pending=%v; want 503 and the deletion recorded", rec.Code, rec.Body.String(), after.DeletedPending)
			}
			fake.mu.Lock()
			fake.failPatch = false
			fake.mu.Unlock()
			rec = e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug, nil, map[string]string{"X-Active-Org": org.Slug})
		}
		if rec.Code != http.StatusNoContent || !after.DeletedPending {
			t.Fatalf("DELETE = %d %s, deleted_pending=%v", rec.Code, rec.Body.String(), after.DeletedPending)
		}
		if got := fake.cancellations(); len(got) != 1 || got[0] != "sub-org" {
			t.Fatalf("cancellations = %v, want the organization's subscription", got)
		}
	}
}
