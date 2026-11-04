package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

const orgSubjectWebhookSecret = "polar_whs_private-org-subject-test-secret"

func deliverOrgSubjectPolar(t *testing.T, h http.Handler, eventType, customer, subscription, status string) int {
	t.Helper()
	body, err := json.Marshal(map[string]any{"type": eventType, "data": map[string]any{"id": subscription, "customer_id": customer, "status": status}})
	if err != nil {
		t.Fatal(err)
	}
	when, id := time.Now(), uuid.NewString()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/polar", bytes.NewReader(body))
	r.Header.Set("webhook-id", id)
	r.Header.Set("webhook-timestamp", strconv.FormatInt(when.Unix(), 10))
	r.Header.Set("webhook-signature", polar.SignForTest(body, orgSubjectWebhookSecret, id, when))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// Hunt 17 H17-3: the billing-mail outboxes lock, transition and mail account
// rows only. With them enabled, every payment failure, recovery and
// subscription end for a company organization failed with 503 forever: its
// dunning never started and a revoked subscription kept its paid plan.
// Organizations take the legacy paths; accounts still take the outboxes.
func TestBillingMailOutboxesLeaveOrganizationsOnLegacyPaths(t *testing.T) {
	ctx := t.Context()
	e := apidserver.Setup(t, api.PlanPro)
	s := *e.S()
	provider, err := polar.NewProvider(polar.Config{APIKey: "private-offline-org-subject-test-token", WebhookSecret: orgSubjectWebhookSecret, Sandbox: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.WithBillingProvider(provider)
	s.RequestBillingMailOutboxesForTest()
	h, store := s.Handler(), *e.Store()

	// MemStore turns resource ownership on for a Free organization.
	org, err := store.CreateOrg(ctx, state.Org{Slug: "billing-co", Name: "Billing Co", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	orgCustomer := "polar-org-" + org.ID
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: org.ID, Provider: "polar", CustomerID: orgCustomer, SubscriptionID: "sub-org"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	orgStatus := func() state.OrgStatus {
		t.Helper()
		got, err := store.OrgByID(ctx, org.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Status
	}

	if code := deliverOrgSubjectPolar(t, h, "subscription.past_due", orgCustomer, "sub-org", "past_due"); code != http.StatusOK || orgStatus() != state.OrgStatusPastDue {
		t.Fatalf("organization payment failure = %d, status %s; want 200 and past_due", code, orgStatus())
	}
	if code := deliverOrgSubjectPolar(t, h, "subscription.active", orgCustomer, "sub-org", "active"); code != http.StatusOK || orgStatus() != state.OrgStatusActive {
		t.Fatalf("organization payment recovery = %d, status %s; want 200 and active", code, orgStatus())
	}
	if code := deliverOrgSubjectPolar(t, h, "subscription.revoked", orgCustomer, "sub-org", "canceled"); code != http.StatusOK {
		t.Fatalf("organization subscription end = %d, want 200", code)
	}
	subject, err := state.BillingResourceAccount(ctx, store, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := store.BillingIdentity(ctx, org.ID, "polar")
	if err != nil {
		t.Fatal(err)
	}
	if subject.Plan != api.PlanFree || identity.SubscriptionID != "" {
		t.Fatalf("after the subscription ended: plan %s, subscription %q; want free and no binding", subject.Plan, identity.SubscriptionID)
	}

	// An account still selects its outbox, which this server has not
	// configured, so the delivery fails closed instead of mailing inline.
	accountCustomer := "polar-acct-" + e.Acct().ID
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: e.Acct().ID, Provider: "polar", CustomerID: accountCustomer, SubscriptionID: "sub-acct"}); err != nil {
		t.Fatal(err)
	}
	if code := deliverOrgSubjectPolar(t, h, "subscription.past_due", accountCustomer, "sub-acct", "past_due"); code != http.StatusServiceUnavailable {
		t.Fatalf("account payment failure with an unconfigured outbox = %d, want 503", code)
	}
}
