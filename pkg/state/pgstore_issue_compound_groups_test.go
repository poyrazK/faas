//go:build !no_pg

// adr: 1662
package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssueCompoundGroupsAndAliases(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule := api.IssueCompoundAlertRule{Key: "group-impact", Enabled: true, MinimumEvents: 2, MinimumCustomers: 2, WindowSeconds: 60, CooldownSeconds: 60}
	zero := int64(0)
	if _, err := s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{rule}}); err != nil {
		t.Fatal(err)
	}
	params := func(key string) state.RecordIssueParams {
		return state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "GroupError", SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: "group test", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
	}
	record := func(key string) (api.IssueEventResponse, state.RecordIssueParams) {
		t.Helper()
		p := params(key)
		out, err := s.RecordIssue(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return out, p
	}
	a, first := record("a")
	record("a")
	b, _ := record("b")
	if _, err := pool.Exec(ctx, "UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2", uuid.NewString(), app.ID); err != nil {
		t.Fatal(err)
	}
	preview := func(id string) api.IssueCompoundAlertPreview {
		t.Helper()
		out, err := s.PreviewIssueCompoundAlert(ctx, app.ID, owner.ID, api.PreviewIssueCompoundAlertRequest{IssueID: id, Rule: rule}, now)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, err := s.ActOnIssue(ctx, app.ID, a.IssueID, owner.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: b.IssueID}, now); err != nil {
		t.Fatal(err)
	}
	if out := preview(a.IssueID); out.IssueID != b.IssueID || out.ObservedEvents != 3 || out.IdentifiedCustomers != 1 || out.CustomersMatched {
		t.Fatal("alias or distinct customers", out)
	}
	if _, err := pool.Exec(ctx, "UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2 AND event_id=$3", uuid.NewString(), app.ID, first.Event.EventID); err != nil {
		t.Fatal(err)
	}
	if out := preview(a.IssueID); !out.Allowed || out.IdentifiedCustomers != 2 {
		t.Fatal(out)
	}
	record("b")
	if out := preview(a.IssueID); out.Reason != "cooldown" {
		t.Fatal("merged group receipt", out)
	}
	if _, err := s.ActOnIssue(ctx, app.ID, b.IssueID, owner.ID, api.IssueActionRequest{Action: "split", MemberIssueID: a.IssueID}, now); err != nil {
		t.Fatal(err)
	}
	if out := preview(a.IssueID); out.IssueID != a.IssueID || out.ObservedEvents != 2 || out.IdentifiedCustomers != 2 || !out.Allowed {
		t.Fatal("split membership and original receipts", out)
	}
	if out := preview(b.IssueID); out.ObservedEvents != 2 || out.IdentifiedCustomers != 1 || out.CooldownReady {
		t.Fatal("canonical receipt leaked or reset", out)
	}
}
