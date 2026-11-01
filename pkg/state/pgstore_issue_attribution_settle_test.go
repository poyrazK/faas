package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgIssueAttributionSettles pins the end of attribution retries: an
// occurrence that names no evidence is checked once, and one that names a
// request is retried until api.IssueAttributionRetryWindow has passed. A
// settled occurrence leaves the retry scan (its check time is infinite).
func TestPgIssueAttributionSettles(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	store := state.IssueStore(s)
	now := time.Now().UTC().Truncate(time.Microsecond)
	hash := []byte(strings.Repeat("s", 32))
	limits := api.PlanPro.IssueLimits()
	if _, err := store.CreateIssueToken(ctx, account.ID, app.ID, api.CreateIssueIngestTokenRequest{
		DeploymentID: deployment.ID,
		Environment:  "application",
		Name:         "attribution-settle",
		ExpiresAt:    now.Add(time.Hour),
	}, hash, limits); err != nil {
		t.Fatalf("CreateIssueToken: %v", err)
	}
	credential, err := store.FindIssueToken(ctx, hash, now)
	if err != nil {
		t.Fatalf("FindIssueToken: %v", err)
	}
	record := func(requestID string) string {
		t.Helper()
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "SettleError", Message: "no customer evidence", SourceKind: "exception", RequestID: requestID}
		if _, err := store.RecordIssue(ctx, state.RecordIssueParams{
			Credential:      credential,
			Event:           event,
			Fingerprint:     strings.Repeat("d", 64),
			Title:           "SettleError: no customer evidence",
			PayloadHash:     strings.Repeat("e", 64),
			GroupingVersion: 1,
			Limits:          limits,
			Now:             now,
		}); err != nil {
			t.Fatalf("RecordIssue: %v", err)
		}
		return event.EventID
	}
	noEvidence := record("")
	awaitingRequest := record(uuid.NewString())
	settled := func(eventID string) bool {
		t.Helper()
		var infinite bool
		if err := pool.QueryRow(ctx, `select attribution_checked_at = 'infinity' from issue_events where app_id = $1 and event_id = $2`, app.ID, eventID).Scan(&infinite); err != nil {
			t.Fatalf("read attribution_checked_at: %v", err)
		}
		return infinite
	}

	if err := s.MaintainIssues(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("MaintainIssues inside the window: %v", err)
	}
	if !settled(noEvidence) {
		t.Fatal("an occurrence that names no evidence was left in the retry scan")
	}
	if settled(awaitingRequest) {
		t.Fatal("an occurrence waiting on request evidence settled inside the retry window")
	}

	if err := s.MaintainIssues(ctx, now.Add(api.IssueAttributionRetryWindow+2*time.Minute)); err != nil {
		t.Fatalf("MaintainIssues past the window: %v", err)
	}
	if !settled(awaitingRequest) {
		t.Fatal("an occurrence past the retry window stayed in the retry scan")
	}
}
