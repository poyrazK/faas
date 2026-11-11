// adr: 20261110182236
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// exerciseAgentRequestReason checks the agent's reason is stored on its
// approval request (kept through a decision, on the webhook events and the
// notification line) and on its activity rows; no reason stores none.
func exerciseAgentRequestReason(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	acct, err := store.CreateAccount(ctx, "agent-reason-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "reason-agent-"+suffix, []string{api.ScopeAgent, api.ScopeDeployWrite})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	const reason = "retire the staging copy of checkout"
	request := state.AgentApproval{
		ID: uuid.NewString(), KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Category: api.AgentApprovalDelete,
		Method: "DELETE", Path: "/v1/apps/checkout-staging", ExpiresAt: time.Now().Add(api.AgentApprovalTTL),
		ReviewURL: "https://gregale.test/review", AgentName: key.Label, Reason: reason,
	}
	opened, err := store.RequestAgentApproval(ctx, request)
	if err != nil {
		t.Fatalf("RequestAgentApproval: %v", err)
	}
	if got, err := store.AgentApproval(ctx, opened.ID); err != nil || got.Reason != reason {
		t.Fatalf("stored reason = %q, %v; want %q", got.Reason, err, reason)
	}
	repeat := request
	repeat.ID, repeat.Reason = uuid.NewString(), "a different story"
	if again, err := store.RequestAgentApproval(ctx, repeat); err != nil || again.ID != opened.ID || again.Reason != reason {
		t.Fatalf("repeat = %+v, %v; want the first request with its reason", again, err)
	}
	if lines, err := store.PendingAgentApprovalNotificationsForKey(ctx, key.ID); err != nil || len(lines) != 1 || lines[0].Reason != reason {
		t.Fatalf("notification lines = %+v, %v; want the reason", lines, err)
	}
	if err := store.DecideAgentApproval(ctx, opened.ID, true, acct.ID, ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got, err := store.AgentApproval(ctx, opened.ID); err != nil || got.Reason != reason {
		t.Fatalf("decided reason = %q, %v; want %q", got.Reason, err, reason)
	}
	listed, err := store.ListAgentApprovalsForKey(ctx, key.ID, 10)
	if err != nil || len(listed) != 1 || listed[0].Reason != reason {
		t.Fatalf("listed = %+v, %v; want the reason", listed, err)
	}

	none := request
	none.ID, none.Path, none.Reason = uuid.NewString(), "/v1/apps/other", ""
	if plain, err := store.RequestAgentApproval(ctx, none); err != nil {
		t.Fatalf("RequestAgentApproval without a reason: %v", err)
	} else if got, err := store.AgentApproval(ctx, plain.ID); err != nil || got.Reason != "" {
		t.Fatalf("reason without one = %q, %v", got.Reason, err)
	}

	for _, row := range []state.AgentActivity{
		{KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Method: "DELETE", Path: "/v1/apps/checkout-staging", Status: 204, ApprovalID: opened.ID, Reason: reason, CreatedAt: time.Now().UTC()},
		{KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Method: "POST", Path: "/v1/apps", Status: 201, CreatedAt: time.Now().UTC().Add(time.Millisecond)},
	} {
		if err := store.RecordAgentActivity(ctx, row); err != nil {
			t.Fatalf("RecordAgentActivity: %v", err)
		}
	}
	rows, err := store.ListAgentActivity(ctx, key.ID, time.Now().Add(-time.Hour), state.AgentActivityCursor{}, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("activity = %+v, %v", rows, err)
	}
	for _, row := range rows {
		want := ""
		if row.ApprovalID == opened.ID {
			want = reason
		}
		if row.Reason != want {
			t.Fatalf("activity row %s %s reason = %q; want %q", row.Method, row.Path, row.Reason, want)
		}
	}
}

func TestMemStoreAgentRequestReason(t *testing.T) {
	exerciseAgentRequestReason(t, state.NewMemStore())
}

func TestPgStoreAgentRequestReason(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentRequestReason(t, state.NewPgStore(pool))
}
