//go:build !no_pg

// adr: 1662
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

func TestPgIssueCompoundAlertsSharedWindowDeliveryAndPreview(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	team, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{owner.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueSlackDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C0123456789", Enabled: true, TokenSealed: []byte("test-sealed")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"compound_threshold_reached"}, FallbackTeamID: team.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: owner.ID, AppID: app.ID, Enabled: true, TargetURL: "https://example.com/compound", EventFilter: []string{"issue.compound_threshold_reached"}, SecretSealed: []byte("test-sealed")}); err != nil {
		t.Fatal(err)
	}
	rule := api.IssueCompoundAlertRule{Key: "payments", Enabled: true, Environment: "application", MinimumEvents: 3, MinimumCustomers: 2, WindowSeconds: 60, CooldownSeconds: 60}
	zero := int64(0)
	policy, err := s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{rule}}); !errors.Is(err, state.ErrIssueCompoundAlertRevision) {
		t.Fatal("stale policy", err)
	}
	params := func(at time.Time) state.RecordIssueParams {
		return state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: at, ExceptionType: "CompoundError", SourceKind: "exception"}, Fingerprint: strings.Repeat("c", 64), Title: "compound test", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}
	}
	record := func(p state.RecordIssueParams) api.IssueEventResponse {
		t.Helper()
		out, err := s.RecordIssue(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	counts := func(want int) {
		t.Helper()
		var activities, receipts, deliveries, outbox int
		for _, q := range []struct {
			sql string
			dst *int
		}{{"SELECT count(*) FROM issue_activity a JOIN app_issues i ON i.id=a.issue_id WHERE i.app_id=$1 AND a.action='compound_threshold_reached'", &activities}, {"SELECT count(*) FROM issue_compound_alert_receipts WHERE app_id=$1", &receipts}, {"SELECT count(*) FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id WHERE dst.app_id=$1 AND d.activity_id IS NOT NULL", &deliveries}, {"SELECT count(*) FROM app_webhook_event_outbox WHERE app_id=$1 AND event='issue.compound_threshold_reached'", &outbox}} {
			if err := pool.QueryRow(ctx, q.sql, app.ID).Scan(q.dst); err != nil {
				t.Fatal(err)
			}
		}
		if activities != want || deliveries != want || outbox != want || receipts != min(want, 1) {
			t.Fatalf("activities=%d receipts=%d deliveries=%d outbox=%d want=%d", activities, receipts, deliveries, outbox, want)
		}
	}
	first := params(now.Add(-time.Second))
	issue := record(first)
	second := params(now.Add(-time.Second))
	record(second)
	// These rows stand in for already verified platform attribution, not reported hints.
	customer := uuid.NewString()
	if _, err = pool.Exec(ctx, "UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2", customer, app.ID); err != nil {
		t.Fatal(err)
	}
	third := params(now)
	record(third)
	counts(0) // Frequency alone cannot satisfy the rule.
	preview := func(r api.IssueCompoundAlertRule) api.IssueCompoundAlertPreview {
		t.Helper()
		p, err := s.PreviewIssueCompoundAlert(ctx, app.ID, owner.ID, api.PreviewIssueCompoundAlertRequest{IssueID: issue.IssueID, Rule: r}, now)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := preview(rule); p.EventsMatched != true || p.CustomersMatched || p.IdentifiedCustomers != 1 || p.ObservedEvents != 3 {
		t.Fatal(p)
	}
	if _, err = pool.Exec(ctx, "UPDATE issue_events SET verified_consumer_id=$1 WHERE app_id=$2 AND event_id=$3", uuid.NewString(), app.ID, third.Event.EventID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if p := preview(rule); !p.Allowed {
			t.Fatal("preview consumed eligibility", p)
		}
	}
	counts(0)
	// A failed outbox insertion must roll back the capture, activity and receipt.
	if _, err = pool.Exec(ctx, `CREATE FUNCTION reject_compound_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event='issue.compound_threshold_reached' THEN RAISE EXCEPTION 'test outbox failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_compound_test BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW EXECUTE FUNCTION reject_compound_test()`); err != nil {
		t.Fatal(err)
	}
	p := params(now)
	if _, err = s.RecordIssue(ctx, p); err == nil {
		t.Fatal("outbox failure was hidden")
	}
	counts(0)
	if _, err = pool.Exec(ctx, "DROP TRIGGER reject_compound_test ON app_webhook_event_outbox; DROP FUNCTION reject_compound_test()"); err != nil {
		t.Fatal(err)
	}
	var concurrent sync.WaitGroup
	captureErrors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		capture := p
		if i > 0 {
			capture = params(now)
		}
		concurrent.Add(1)
		go func() { defer concurrent.Done(); _, err := s.RecordIssue(ctx, capture); captureErrors <- err }()
	}
	concurrent.Wait()
	close(captureErrors)
	for err := range captureErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	counts(1)
	claim, claimed, claimErr := s.ClaimIssueSlackDelivery(ctx)
	if claimErr != nil || !claimed || !claim.Application || claim.Kind != "compound_threshold_reached" || claim.Details["identified_customers"] != "2" {
		t.Fatal("compound delivery was not claimable", claim, claimed, claimErr)
	}
	record(p)
	record(params(now))
	counts(1) // Retry and a new event remain inside cooldown.
	if p := preview(rule); p.Reason != "cooldown" || p.NextEligibleAt == nil {
		t.Fatal(p)
	}
	if _, err = s.PreviewIssueCompoundAlert(ctx, app.ID, uuid.NewString(), api.PreviewIssueCompoundAlertRequest{IssueID: issue.IssueID, Rule: rule}, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign account preview", err)
	}
	// Enable toggles keep the rule's receipt; semantic changes use a new hash.
	rule.Enabled = false
	policy, err = s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &policy.Revision, Rules: []api.IssueCompoundAlertRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	rule.Enabled = true
	policy, err = s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &policy.Revision, Rules: []api.IssueCompoundAlertRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	if p := preview(rule); p.CooldownReady {
		t.Fatal("toggle reset receipt", p)
	}
	// Both counts exclude exactly the lower boundary and include the upper one.
	future := now.Add(60 * time.Second)
	if p, err := s.PreviewIssueCompoundAlert(ctx, app.ID, owner.ID, api.PreviewIssueCompoundAlertRequest{IssueID: issue.IssueID, Rule: rule}, future); err != nil || p.ObservedEvents != 0 || p.IdentifiedCustomers != 0 {
		t.Fatal("shared boundary", p, err)
	}
}

func TestPgIssueCompoundAlertMigrationReplayAndRollback(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	raw, err := migrations.FS.ReadFile("20261017235322479_issue_compound_alert_rules.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err = pool.Exec(ctx, parts[0]); err != nil {
			t.Fatal("replay", err)
		}
	}
	zero := int64(0)
	rule := api.IssueCompoundAlertRule{Key: "impact", Enabled: true, MinimumEvents: 1, MinimumCustomers: 1, WindowSeconds: 60, CooldownSeconds: 60}
	policy, err := s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("populated configuration rollback erased rules")
	}
	if _, err = s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &policy.Revision, Rules: []api.IssueCompoundAlertRule{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO issue_slack_routing_policies(app_id,account_id,revision,installation_revision,rules) VALUES($1,$2,1,1,'[{"event":"compound_threshold_reached"}]')`, app.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("routing-only compound configuration allowed rollback")
	}
	if _, err = pool.Exec(ctx, "DELETE FROM issue_slack_routing_policies WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("up after down", err)
	}
	var definition string
	if err = pool.QueryRow(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='issue_activity'::regclass AND conname='issue_activity_action_check'").Scan(&definition); err != nil || !strings.Contains(definition, "compound_threshold_reached") {
		t.Fatal(definition, err)
	}
}

func TestPgIssueCompoundAlertConcurrentPolicyWrites(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	zero := int64(0)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.SetIssueCompoundAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueCompoundAlertRule{{Key: key, Enabled: true, MinimumEvents: 1, MinimumCustomers: 1, WindowSeconds: 60, CooldownSeconds: 60}}})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, state.ErrIssueCompoundAlertRevision) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
