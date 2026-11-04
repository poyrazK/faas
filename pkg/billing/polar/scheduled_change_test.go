package polar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

const scheduledSubscription = `{"id":"sub-1","status":"active","product_id":"pro-product","current_period_end":"2026-12-01T00:00:00Z",` +
	`"cancel_at_period_end":true,"ends_at":"2026-12-01T00:00:00Z","pending_update":{"applies_at":"2026-12-01T00:00:00Z","product_id":"hobby-product"}}`

// scheduledServer answers GET /v1/subscriptions/sub-1 with get() and records
// every PATCH body, answering it with patch().
func scheduledServer(t *testing.T, get func() string, patch func(body map[string]any) (int, string)) (*Provider, *[]map[string]any) {
	t.Helper()
	var patches []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions/sub-1":
			_, _ = io.WriteString(w, get())
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/subscriptions/sub-1":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			patches = append(patches, body)
			status, out := patch(body)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, out)
		default:
			t.Errorf("unexpected Polar call %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, &patches
}

// The subscription state carries a scheduled cancellation and a scheduled
// plan change, which billing status reports.
// adr: 20261103131742
func TestSubscriptionStateReportsScheduledChanges(t *testing.T) {
	p, _ := scheduledServer(t, func() string { return scheduledSubscription }, nil)
	got, err := p.SubscriptionState(context.Background(), "sub-1")
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	if !got.CancelAtPeriodEnd || !got.EndsAt.Equal(end) || got.PendingPlanID != string(api.PlanHobby) || !got.PendingAt.Equal(end) || !got.ScheduledChange() {
		t.Fatalf("state = %+v", got)
	}
}

// Resuming undoes the scheduled cancellation and clears the scheduled plan
// change, with the bodies Polar's API takes; a subscription with nothing
// scheduled is left alone, and one that ended is gone.
// adr: 20261103131742
func TestResumeSubscriptionUndoesScheduledChanges(t *testing.T) {
	acct := state.Account{ID: "acct-1", StripeSubscriptionItem: "sub-1"}
	ok := func(map[string]any) (int, string) { return http.StatusOK, `{"id":"sub-1","status":"active"}` }

	p, patches := scheduledServer(t, func() string { return scheduledSubscription }, ok)
	undone, err := p.ResumeSubscription(context.Background(), acct)
	if err != nil || !undone {
		t.Fatalf("ResumeSubscription = %v, %v; want undone", undone, err)
	}
	if len(*patches) != 2 || (*patches)[0]["cancel_at_period_end"] != false {
		t.Fatalf("patches = %v, want cancel_at_period_end=false then pending_update=null", *patches)
	}
	if v, present := (*patches)[1]["pending_update"]; !present || v != nil {
		t.Fatalf("second patch = %v, want pending_update=null", (*patches)[1])
	}

	p, patches = scheduledServer(t, func() string {
		return `{"id":"sub-1","status":"active","product_id":"pro-product","cancel_at_period_end":false,"pending_update":null}`
	}, ok)
	if undone, err := p.ResumeSubscription(context.Background(), acct); err != nil || undone || len(*patches) != 0 {
		t.Fatalf("nothing scheduled: undone=%v err=%v patches=%v, want no change", undone, err, *patches)
	}

	p, _ = scheduledServer(t, func() string { return `{"id":"sub-1","status":"canceled","product_id":"pro-product"}` }, ok)
	if _, err := p.ResumeSubscription(context.Background(), acct); !errors.Is(err, billing.ErrSubscriptionGone) {
		t.Fatalf("ended subscription: err = %v, want ErrSubscriptionGone", err)
	}
}

// Polar's refusals of a plan change come back typed, so apid answers each
// one: a subscription scheduled to end, a charge the bank must confirm, and
// a declined charge.
// adr: 20261103131742
func TestPlanChangeRefusalsAreTyped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		from   api.Plan
		to     api.Plan
		status int
		body   string
		want   error
	}{
		{"ending upgrade", api.PlanHobby, api.PlanPro, http.StatusForbidden, `{"error":"AlreadyCanceledSubscription","detail":"x"}`, billing.ErrSubscriptionEnding},
		{"ending downgrade", api.PlanPro, api.PlanHobby, http.StatusForbidden, `{"error":"AlreadyCanceledSubscription","detail":"x"}`, billing.ErrSubscriptionEnding},
		{"bank confirmation", api.PlanHobby, api.PlanPro, http.StatusPaymentRequired, `{"error":"PaymentActionRequired","detail":"x"}`, billing.ErrPaymentAuthenticationRequired},
		{"declined", api.PlanHobby, api.PlanPro, http.StatusPaymentRequired, `{"error":"PaymentFailed","detail":"x"}`, billing.ErrPaymentDeclined},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := scheduledServer(t, nil, func(map[string]any) (int, string) { return tc.status, tc.body })
			_, err := p.ChangeSubscriptionPlan(context.Background(), state.Account{ID: "acct-1", Plan: tc.from, StripeSubscriptionItem: "sub-1"}, tc.to)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Polar refuses to cancel twice. A second cancellation is ErrAlreadyCancelled,
// and a downgrade to Free on a subscription already ending returns its end.
// adr: 20261103131742
func TestCancellingASubscriptionThatIsEnding(t *testing.T) {
	refuse := func(map[string]any) (int, string) {
		return http.StatusForbidden, `{"error":"AlreadyCanceledSubscription","detail":"x"}`
	}
	p, _ := scheduledServer(t, func() string { return scheduledSubscription }, refuse)
	acct := state.Account{ID: "acct-1", Plan: api.PlanPro, StripeSubscriptionItem: "sub-1"}
	if _, err := p.CancelAtPeriodEnd(context.Background(), acct); !errors.Is(err, billing.ErrAlreadyCancelled) {
		t.Fatalf("second cancel: err = %v, want ErrAlreadyCancelled", err)
	}
	endsAt, err := p.ChangeSubscriptionPlan(context.Background(), acct, api.PlanFree)
	if err != nil || !endsAt.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("downgrade to Free while ending = %v, %v; want the scheduled end", endsAt, err)
	}
}
