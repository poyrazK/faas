package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the PgStore boundary directly so the exact-package coverage gate
// includes the release-aware Issues persistence paths, not only their callers.
func TestPg_IssueStoreLifecycle(t *testing.T) {
	s, ctx := pgStore(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	store := state.IssueStore(s)
	now := time.Now().UTC().Truncate(time.Microsecond)
	hash := []byte(strings.Repeat("h", 32))
	limits := api.PlanPro.IssueLimits()

	token, err := store.CreateIssueToken(ctx, account.ID, app.ID, api.CreateIssueIngestTokenRequest{
		DeploymentID: deployment.ID,
		Environment:  "application",
		Name:         "coverage-test",
		ExpiresAt:    now.Add(time.Hour),
	}, hash, limits)
	if err != nil {
		t.Fatalf("CreateIssueToken: %v", err)
	}
	credential, err := store.FindIssueToken(ctx, hash, now)
	if err != nil || credential.ID != token.ID || credential.AppID != app.ID {
		t.Fatalf("FindIssueToken = %+v, %v", credential, err)
	}
	if _, err := store.FindIssueToken(ctx, []byte("unknown"), now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown token error = %v, want ErrNotFound", err)
	}
	tokens, err := store.ListIssueTokens(ctx, app.ID)
	if err != nil || len(tokens) != 1 || tokens[0].ID != token.ID {
		t.Fatalf("ListIssueTokens = %+v, %v", tokens, err)
	}

	event := api.IssueEvent{
		EventID:       uuid.NewString(),
		OccurredAt:    now,
		ExceptionType: "DateFormatError",
		Message:       "invalid customer date format",
		Frames:        []api.IssueFrame{{File: "export.go", Function: "generate", Line: 12, InApp: true}},
	}
	in := state.RecordIssueParams{
		Credential:      credential,
		Event:           event,
		Fingerprint:     strings.Repeat("a", 64),
		Title:           "DateFormatError: invalid customer date format",
		PayloadHash:     strings.Repeat("b", 64),
		GroupingVersion: 1,
		Limits:          limits,
		Now:             now,
	}
	first, err := store.RecordIssue(ctx, in)
	if err != nil || first.IssueID == "" || first.Duplicate {
		t.Fatalf("RecordIssue first = %+v, %v", first, err)
	}
	duplicate, err := store.RecordIssue(ctx, in)
	if err != nil || !duplicate.Duplicate || duplicate.IssueID != first.IssueID {
		t.Fatalf("RecordIssue duplicate = %+v, %v", duplicate, err)
	}
	conflict := in
	conflict.PayloadHash = strings.Repeat("c", 64)
	if _, err := store.RecordIssue(ctx, conflict); !errors.Is(err, state.ErrIssueEventConflict) {
		t.Fatalf("reused event ID error = %v, want ErrIssueEventConflict", err)
	}

	listed, err := store.ListIssues(ctx, app.ID, state.IssueListFilter{State: "open", Environment: "application"}, state.IssueCursor{})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != first.IssueID {
		t.Fatalf("ListIssues = %+v, %v", listed, err)
	}
	detail, err := store.GetIssueDetail(ctx, app.ID, first.IssueID, now.Add(-time.Hour), now.Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || len(detail.Events) != 1 || len(detail.Releases) != 1 || detail.Impact.ObservedEvents != 1 {
		t.Fatalf("GetIssueDetail = %+v, %v", detail, err)
	}

	assigned, err := store.ActOnIssue(ctx, app.ID, first.IssueID, account.ID, api.IssueActionRequest{Action: "assign", AssigneeAccountID: account.ID}, now)
	if err != nil || assigned.AssigneeAccountID != account.ID {
		t.Fatalf("assign issue = %+v, %v", assigned, err)
	}
	resolved, err := store.ActOnIssue(ctx, app.ID, first.IssueID, account.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: deployment.ID}, now.Add(time.Second))
	if err != nil || resolved.State != "resolved" || resolved.ResolvedAt == nil {
		t.Fatalf("resolve issue = %+v, %v", resolved, err)
	}
	reopened, err := store.ActOnIssue(ctx, app.ID, first.IssueID, account.ID, api.IssueActionRequest{Action: "reopen"}, now.Add(2*time.Second))
	if err != nil || reopened.State != "open" {
		t.Fatalf("reopen issue = %+v, %v", reopened, err)
	}
	ignoredUntil := now.Add(2 * time.Minute)
	if _, err := store.ActOnIssue(ctx, app.ID, first.IssueID, account.ID, api.IssueActionRequest{Action: "ignore", IgnoredUntil: &ignoredUntil}, now.Add(time.Second)); err != nil {
		t.Fatalf("ignore issue: %v", err)
	}
	if err := s.MaintainIssues(ctx, ignoredUntil.Add(time.Second)); err != nil {
		t.Fatalf("MaintainIssues: %v", err)
	}
	if err := store.RevokeIssueToken(ctx, app.ID, token.ID); err != nil {
		t.Fatalf("RevokeIssueToken: %v", err)
	}
	if _, err := store.FindIssueToken(ctx, hash, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("revoked token error = %v, want ErrNotFound", err)
	}
}
