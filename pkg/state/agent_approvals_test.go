package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104140058 — an agent key's approval categories are set once; a
// request is reused while pending, a person's approval allows exactly one
// matching request, and a denied, expired or differently bound request
// allows none.
func exerciseAgentApprovals(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	acct, err := store.CreateAccount(ctx, "agent-approvals-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "agent-"+suffix, []string{api.ScopeAgent, api.ScopeDeployWrite})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if categories, set, err := store.APIKeyApprovalCategories(ctx, key.ID); err != nil || set || len(categories) != 0 {
		t.Fatalf("categories of a fresh key = %v, %v, %v; want none set", categories, set, err)
	}
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{"everything"}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("an unknown category = %v, want ErrConflict", err)
	}
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{}); err != nil {
		t.Fatalf("SetAPIKeyApprovalCategories(none): %v", err)
	}
	if categories, set, err := store.APIKeyApprovalCategories(ctx, key.ID); err != nil || !set || len(categories) != 0 {
		t.Fatalf("categories = %v, %v, %v; want an empty set", categories, set, err)
	}
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{api.AgentApprovalDelete}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changing the categories = %v, want ErrConflict", err)
	}

	// adr: 20261107140000 — an account receiver subscribed to the approval
	// events gets one delivery per new request and per decision.
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{
		AccountID: acct.ID, Scope: state.AppWebhookScopeAccount, TargetURL: "https://example.com/approvals",
		SecretSealed: []byte("sealed"), RetryPolicy: state.AppWebhookRetryDefault, Enabled: true,
		EventFilter: []string{api.AgentApprovalRequestedWebhookEvent, api.AgentApprovalDeniedWebhookEvent},
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAccountReleaseWebhookIfUnderQuota: %v", err)
	}
	approvalEvents := func(event string) []api.AgentApprovalWebhookPayload {
		t.Helper()
		deliveries, _, err := store.ListAccountReleaseWebhookDeliveries(ctx, acct.ID, hook.ID, 200, "")
		if err != nil {
			t.Fatalf("ListAccountReleaseWebhookDeliveries: %v", err)
		}
		var out []api.AgentApprovalWebhookPayload
		for _, delivery := range deliveries {
			if string(delivery.Event) != event {
				continue
			}
			var payload api.AgentApprovalWebhookPayload
			if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
				t.Fatalf("payload %s: %v", delivery.Payload, err)
			}
			out = append(out, payload)
		}
		return out
	}

	body := sha256.Sum256([]byte(`{"confirm":true}`))
	request := state.AgentApproval{
		KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Category: api.AgentApprovalDelete,
		Method: "DELETE", Path: "/v1/apps/web", BodySHA256: body[:], ExpiresAt: time.Now().Add(api.AgentApprovalTTL),
	}
	binding := state.AgentApprovalBinding{KeyID: key.ID, Method: "DELETE", Path: "/v1/apps/web", BodySHA256: body[:]}
	withLink := request
	withLink.ID = uuid.NewString()
	withLink.ReviewURL = "https://example.com/review/" + withLink.ID
	first, err := store.RequestAgentApproval(ctx, withLink)
	if err == nil && first.ID != withLink.ID {
		t.Fatalf("a new request took id %s, want the caller's %s", first.ID, withLink.ID)
	}
	if requested := approvalEvents(api.AgentApprovalRequestedWebhookEvent); len(requested) != 1 || requested[0].ApprovalID != withLink.ID ||
		requested[0].ReviewURL != withLink.ReviewURL || requested[0].Status != api.AgentApprovalPending || requested[0].Category != api.AgentApprovalDelete {
		t.Fatalf("requested events = %+v; want one for the new request with its review link", requested)
	}
	if _, err := store.RequestAgentApproval(ctx, request); err != nil {
		t.Fatalf("repeat request: %v", err)
	}
	if requested := approvalEvents(api.AgentApprovalRequestedWebhookEvent); len(requested) != 1 {
		t.Fatalf("a reused request enqueued again: %+v", requested)
	}
	if err != nil {
		t.Fatalf("RequestAgentApproval: %v", err)
	}
	if first.Status != api.AgentApprovalPending || first.ID == "" {
		t.Fatalf("new request = %+v, want pending", first)
	}
	again, err := store.RequestAgentApproval(ctx, request)
	if err != nil || again.ID != first.ID {
		t.Fatalf("repeated request = %v, %v; want the pending %s", again.ID, err, first.ID)
	}
	if _, ok, err := store.ConsumeAgentApproval(ctx, binding); err != nil || ok {
		t.Fatalf("consuming an undecided request = %v, %v; want nothing", ok, err)
	}
	pending, err := store.ListPendingAgentApprovals(ctx, acct.ID, nil, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != first.ID {
		t.Fatalf("pending for the connector = %v, %v", pending, err)
	}
	if others, err := store.ListPendingAgentApprovals(ctx, "00000000-0000-0000-0000-000000000001", []string{key.OrgID}, 10); err != nil || len(others) != 1 {
		t.Fatalf("pending for an org admin = %v, %v; want the request", others, err)
	}

	if err := store.DecideAgentApproval(ctx, first.ID, true, acct.ID, ""); err != nil {
		t.Fatalf("DecideAgentApproval: %v", err)
	}
	if err := store.DecideAgentApproval(ctx, first.ID, false, acct.ID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deciding twice = %v, want ErrNotFound", err)
	}
	other := binding
	other.Path = "/v1/apps/api"
	if _, ok, err := store.ConsumeAgentApproval(ctx, other); err != nil || ok {
		t.Fatalf("consuming for another path = %v, %v; want nothing", ok, err)
	}
	unbound := binding
	unbound.BodySHA256 = nil
	if _, ok, err := store.ConsumeAgentApproval(ctx, unbound); err != nil || ok {
		t.Fatalf("consuming without the bound body = %v, %v; want nothing", ok, err)
	}
	if id, ok, err := store.ConsumeAgentApproval(ctx, binding); err != nil || !ok || id != first.ID {
		t.Fatalf("consuming the approval = %v, %v, %v; want %s", id, ok, err, first.ID)
	}
	if _, ok, err := store.ConsumeAgentApproval(ctx, binding); err != nil || ok {
		t.Fatalf("consuming it twice = %v, %v; want nothing", ok, err)
	}
	used, err := store.AgentApproval(ctx, first.ID)
	if err != nil || used.Status != api.AgentApprovalConsumed || used.DecidedBy != acct.ID {
		t.Fatalf("consumed request = %+v, %v", used, err)
	}
	// adr: 20261111150000 — a windowed rule finds the approval it used
	// within the window of its first use, for the same binding only.
	if id, ok, err := store.AgentApprovalUsedWithin(ctx, binding, time.Hour); err != nil || !ok || id != first.ID {
		t.Fatalf("used within the window = %v, %v, %v; want %s", id, ok, err, first.ID)
	}
	if _, ok, err := store.AgentApprovalUsedWithin(ctx, other, time.Hour); err != nil || ok {
		t.Fatalf("another binding within the window = %v, %v; want nothing", ok, err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, ok, err := store.AgentApprovalUsedWithin(ctx, binding, 5*time.Millisecond); err != nil || ok {
		t.Fatalf("past the window = %v, %v; want nothing", ok, err)
	}

	denied, err := store.RequestAgentApproval(ctx, request)
	if err != nil || denied.ID == first.ID {
		t.Fatalf("a request after the approval was used = %v, %v; want a new one", denied.ID, err)
	}
	if outcome, ok, err := store.RecentAgentApprovalOutcome(ctx, binding, api.AgentApprovalOutcomeWindow); err != nil || ok {
		t.Fatalf("outcome while the latest request is pending = %+v, %v, %v; want none", outcome, ok, err)
	}
	if err := store.DecideAgentApproval(ctx, denied.ID, false, acct.ID, "  not on a Friday  "); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if deniedEvents := approvalEvents(api.AgentApprovalDeniedWebhookEvent); len(deniedEvents) != 1 || deniedEvents[0].ApprovalID != denied.ID ||
		deniedEvents[0].Note != "not on a Friday" || deniedEvents[0].DecidedAt == "" || deniedEvents[0].ReviewURL != "" {
		t.Fatalf("denied events = %+v; want one with the note", deniedEvents)
	}
	if approved := approvalEvents(api.AgentApprovalApprovedWebhookEvent); len(approved) != 0 {
		t.Fatalf("the receiver did not subscribe to approved, got %+v", approved)
	}
	if _, ok, err := store.ConsumeAgentApproval(ctx, binding); err != nil || ok {
		t.Fatalf("consuming a denied request = %v, %v; want nothing", ok, err)
	}
	// adr: 20261107140000 — the agent repeating a denied request learns the
	// outcome and the person's note, within the window only.
	outcome, ok, err := store.RecentAgentApprovalOutcome(ctx, binding, api.AgentApprovalOutcomeWindow)
	if err != nil || !ok || outcome.ID != denied.ID || outcome.Status != api.AgentApprovalDenied || outcome.Note != "not on a Friday" {
		t.Fatalf("outcome of a denied request = %+v, %v, %v; want it denied with its trimmed note", outcome, ok, err)
	}
	if got, err := store.AgentApproval(ctx, denied.ID); err != nil || got.Note != "not on a Friday" {
		t.Fatalf("denied request = %+v, %v; want its note", got, err)
	}
	if _, ok, err := store.RecentAgentApprovalOutcome(ctx, binding, time.Nanosecond); err != nil || ok {
		t.Fatalf("outcome outside the window = %v, %v; want none", ok, err)
	}
	neverAsked := binding
	neverAsked.Path = "/v1/apps/never-asked"
	if _, ok, err := store.RecentAgentApprovalOutcome(ctx, neverAsked, api.AgentApprovalOutcomeWindow); err != nil || ok {
		t.Fatalf("outcome of a request never made = %v, %v; want none", ok, err)
	}

	expired := request
	expired.Path, expired.BodySHA256 = "/v1/apps/old", nil
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	stale, err := store.RequestAgentApproval(ctx, expired)
	if err != nil {
		t.Fatalf("RequestAgentApproval(expired): %v", err)
	}
	if got := stale.EffectiveStatus(time.Now()); got != api.AgentApprovalExpired {
		t.Fatalf("status of an expired request = %q", got)
	}
	staleBinding := state.AgentApprovalBinding{KeyID: key.ID, Method: expired.Method, Path: expired.Path}
	if outcome, ok, err := store.RecentAgentApprovalOutcome(ctx, staleBinding, api.AgentApprovalOutcomeWindow); err != nil || !ok || outcome.ID != stale.ID || outcome.Note != "" {
		t.Fatalf("outcome of an expired request = %+v, %v, %v; want it", outcome, ok, err)
	}
	if err := store.DecideAgentApproval(ctx, stale.ID, true, acct.ID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("approving an expired request = %v, want ErrNotFound", err)
	}

	for i := 0; i < api.MaxPendingAgentApprovals; i++ {
		flood := request
		flood.Path = "/v1/apps/flood-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if _, err := store.RequestAgentApproval(ctx, flood); err != nil {
			if errors.Is(err, state.ErrTooManyAgentApprovals) && i > 0 {
				break
			}
			t.Fatalf("request %d: %v", i, err)
		}
	}
	flood := request
	flood.Path = "/v1/apps/one-too-many"
	if _, err := store.RequestAgentApproval(ctx, flood); !errors.Is(err, state.ErrTooManyAgentApprovals) {
		t.Fatalf("a request past the pending cap = %v, want ErrTooManyAgentApprovals", err)
	}

	history, err := store.ListAgentApprovalsForKey(ctx, key.ID, 100)
	if err != nil || !slices.ContainsFunc(history, func(a state.AgentApproval) bool { return a.ID == first.ID }) {
		t.Fatalf("history = %d rows, %v", len(history), err)
	}

	if err := store.DeleteAPIKey(ctx, acct.ID, key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}
	if _, err := store.AgentApproval(ctx, first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a deleted key's request = %v, want ErrNotFound", err)
	}
}

func TestMemStoreAgentApprovals(t *testing.T) {
	exerciseAgentApprovals(t, state.NewMemStore())
}

func TestPgStoreAgentApprovals(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentApprovals(t, state.NewPgStore(pool))
}
