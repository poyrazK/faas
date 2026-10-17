//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgIssueLifecycleAlertAdmissionAndPreview(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: owner.ID, AppID: app.ID, Enabled: true, TargetURL: "https://example.com/audit", EventFilter: []string{"issue.created", "issue.regressed"}, SecretSealed: []byte("test-sealed")}); err != nil {
		t.Fatal(err)
	}
	team, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{owner.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueSlackDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C0123456789", Enabled: true, TokenSealed: []byte("test-sealed")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created", "regressed"}, FallbackTeamID: team.ID}); err != nil {
		t.Fatal(err)
	}
	policy, err := s.GetIssueLifecycleAlertPolicy(ctx, app.ID, owner.ID)
	if err != nil || policy.Revision != 0 || policy.CooldownSeconds != 0 || len(policy.Environments) != 0 {
		t.Fatal(policy, err)
	}
	save := func(names []string, cooldown int64) {
		t.Helper()
		policy, err = s.SetIssueLifecycleAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &policy.Revision, Environments: names, CooldownSeconds: cooldown})
		if err != nil {
			t.Fatal(err)
		}
	}
	save([]string{"application"}, 60)
	counts := func(want int) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id WHERE dst.app_id=$1 AND d.activity_id IS NOT NULL", app.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("deliveries=%d want=%d", count, want)
		}
	}
	params := func(key string, at time.Time) state.RecordIssueParams {
		return state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: at, ExceptionType: "LifecycleError", SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: "lifecycle test", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: at}
	}
	record := func(p state.RecordIssueParams) api.IssueEventResponse {
		t.Helper()
		out, err := s.RecordIssue(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	p := params("a", now)
	first := record(p)
	counts(1)
	if !record(p).Duplicate {
		t.Fatal("retry was not duplicate")
	}
	counts(1)
	preview := func(id string, names []string, cooldown int64, at time.Time, reason string) api.IssueLifecycleAlertPreview {
		t.Helper()
		out, err := s.PreviewIssueLifecycleAlert(ctx, app.ID, owner.ID, api.PreviewIssueLifecycleAlertRequest{IssueID: id, Event: "regressed", Environments: names, CooldownSeconds: cooldown}, at)
		if err != nil || out.Reason != reason || out.Allowed != (reason == "eligible") {
			t.Fatalf("preview=%+v err=%v", out, err)
		}
		return out
	}
	blocked := preview(first.IssueID, []string{"application"}, 60, now.Add(59*time.Second), "cooldown")
	if blocked.NextEligibleAt == nil || !blocked.NextEligibleAt.Equal(now.Add(time.Minute)) {
		t.Fatal(blocked)
	}
	preview(first.IssueID, []string{}, 60, now.Add(time.Minute), "eligible")
	preview(first.IssueID, []string{"staging"}, 0, now, "environment_excluded")
	counts(1)
	current, err := s.GetIssueLifecycleAlertPolicy(ctx, app.ID, owner.ID)
	if err != nil || current.Revision != policy.Revision || current.Environments[0] != "application" {
		t.Fatal("preview saved draft", current, err)
	}
	// Resolve and ingest from a newer deployment: created and regressed share
	// one admission receipt, while raw lifecycle activity still records both.
	dep2 := seedPgDeployment(t, s, ctx, app)
	regress := func(at time.Time) {
		t.Helper()
		if _, err := s.ActOnIssue(ctx, app.ID, first.IssueID, owner.ID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, at.Add(-time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		p := params("a", at)
		p.Credential.DeploymentID = dep2.ID
		if !record(p).Regressed {
			t.Fatal("new deployment did not regress resolved issue")
		}
	}
	regress(now.Add(59 * time.Second))
	counts(1)
	regress(now.Add(time.Minute))
	counts(2)
	save([]string{"staging"}, 60)
	excluded := record(params("b", now.Add(2*time.Minute)))
	counts(2)
	// A filtered issue has no cooldown receipt.
	preview(excluded.IssueID, []string{}, 60, now.Add(2*time.Minute), "eligible")
	var activity, transitions int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM issue_activity WHERE issue_id=$1 AND action IN ('created','regressed')", first.IssueID).Scan(&activity); err != nil || activity != 3 {
		t.Fatal("lifecycle activity suppressed", activity, err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM app_webhook_event_outbox WHERE app_id=$1", app.ID).Scan(&transitions); err != nil || transitions < 4 {
		t.Fatal("audit transition suppressed", transitions, err)
	}
	// Policy edits and no-op saves preserve existing cooldowns.
	revision := policy.Revision
	save([]string{"staging"}, 60)
	if policy.Revision != revision {
		t.Fatal("no-op bumped revision")
	}
	save([]string{}, 60)
	preview(first.IssueID, []string{}, 60, now.Add(61*time.Second), "cooldown")
	second := record(params("c", now.Add(90*time.Second)))
	counts(3)
	if _, err = s.ActOnIssue(ctx, app.ID, first.IssueID, owner.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: second.IssueID}, now.Add(91*time.Second)); err != nil {
		t.Fatal(err)
	}
	merged := preview(first.IssueID, []string{}, 60, now.Add(100*time.Second), "cooldown")
	if merged.IssueID != second.IssueID || merged.NextEligibleAt == nil || !merged.NextEligibleAt.Equal(now.Add(150*time.Second)) {
		t.Fatal("merge lost latest receipt", merged)
	}
	if _, err = s.ActOnIssue(ctx, app.ID, second.IssueID, owner.ID, api.IssueActionRequest{Action: "split", MemberIssueID: first.IssueID}, now.Add(101*time.Second)); err != nil {
		t.Fatal(err)
	}
	preview(first.IssueID, []string{}, 60, now.Add(121*time.Second), "eligible")
	preview(second.IssueID, []string{}, 60, now.Add(121*time.Second), "cooldown")
	// A new capture without any route must not reserve a receipt.
	slack, err := s.GetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Revision: slack.Revision, Enabled: true, Events: []string{"created", "regressed"}}); err != nil {
		t.Fatal(err)
	}
	unrouted := record(params("d", now.Add(3*time.Minute)))
	preview(unrouted.IssueID, []string{}, 60, now.Add(3*time.Minute), "eligible")
	// Concurrent writes of one expected revision have exactly one winner.
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	for _, cooldown := range []int64{120, 180} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.SetIssueLifecycleAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &policy.Revision, Environments: []string{}, CooldownSeconds: cooldown})
			errorsOut <- e
		}()
	}
	wg.Wait()
	close(errorsOut)
	winners, conflicts := 0, 0
	for e := range errorsOut {
		if e == nil {
			winners++
		} else if errors.Is(e, state.ErrIssueLifecycleAlertRevision) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal(winners, conflicts)
	}
	if _, err = s.GetIssueLifecycleAlertPolicy(ctx, app.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign policy read", err)
	}
	if _, err = s.PreviewIssueLifecycleAlert(ctx, app.ID, uuid.NewString(), api.PreviewIssueLifecycleAlertRequest{IssueID: first.IssueID, Event: "created", Environments: []string{}}, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign preview", err)
	}
}

func TestPgIssueLifecycleAlertMigrationReplayAndRollback(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	raw, err := migrations.FS.ReadFile("20261017134600148_issue_lifecycle_alert_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing rollback")
	}
	for range 2 {
		if _, err = pool.Exec(ctx, parts[0]); err != nil {
			t.Fatal("replay", err)
		}
	}
	zero := int64(0)
	policy, err := s.SetIssueLifecycleAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &zero, Environments: []string{}, CooldownSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueLifecycleAlertPolicy(ctx, app.ID, uuid.NewString(), api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &policy.Revision, Environments: []string{}, CooldownSeconds: 60}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign policy write", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, downErr := tx.Exec(ctx, parts[1])
	_ = tx.Rollback(ctx)
	if downErr == nil || !strings.Contains(downErr.Error(), "restore the default lifecycle alert policy") {
		t.Fatal("active policy rollback was not refused", downErr)
	}
	if _, err = s.SetIssueLifecycleAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueLifecycleAlertPolicyRequest{ExpectedRevision: &policy.Revision, Environments: []string{}, CooldownSeconds: 0}); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal("default rollback", err)
	}
	for range 2 {
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal("upgrade replay", err)
		}
	}
}
