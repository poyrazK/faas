package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_SlackRuleRoutingPreviewThreadsAndFences(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec("UPDATE accounts SET plan='pro' WHERE id=$1", owner.ID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	org, e := s.CreateOrg(ctx, state.Org{Slug: "routing-" + uuid.NewString()[:8], Name: "Routing", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if e != nil {
		t.Fatal(e)
	}
	member, e := s.CreateAccount(ctx, "routing-"+uuid.NewString()+"@example.com", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.CreateAccount(ctx, "routing-other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{owner.ID, member.ID, other.ID} {
		if e = s.AddOrgMember(ctx, org.ID, id, state.OrgRoleDeveloper, nil); e != nil {
			t.Fatal(e)
		}
	}
	exec("UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID)
	// Non-application issue environments require a retained project release proof.
	projectID := uuid.NewString()
	exec("INSERT INTO projects(id,account_id,slug,org_id) VALUES($1,$2,$3,$4)", projectID, owner.ID, "routing-"+uuid.NewString()[:8], org.ID)
	for _, env := range []string{"production", "staging"} {
		exec("INSERT INTO project_environments(account_id,project_id,slug,protected) VALUES($1,$2,$3,false)", owner.ID, projectID, env)
		releaseID := uuid.NewString()
		exec("INSERT INTO project_release_sets(id,account_id,project_id,environment_slug,ttl_seconds,expires_at) VALUES($1,$2,$3,$4,60,clock_timestamp()+interval '1 hour')", releaseID, owner.ID, projectID, env)
		exec("INSERT INTO project_release_members(release_id,app_id,deployment_id) VALUES($1,$2,$3)", releaseID, app.ID, dep.ID)
	}

	team, e := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "primary", api.PutIssueTeamRequest{Name: "Primary", MemberAccountIDs: []string{member.ID}}, now)
	if e != nil {
		t.Fatal(e)
	}
	backup, e := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "other", api.PutIssueTeamRequest{Name: "Other", MemberAccountIDs: []string{other.ID}}, now)
	if e != nil {
		t.Fatal(e)
	}
	install := func(rev int64) {
		t.Helper()
		f := state.IssueSlackOAuthFlow{AppID: app.ID, AccountID: owner.ID, Hash: issueslack.OAuthStateHash(uuid.NewString()), ExpectedRevision: rev, Sealed: []byte("sealed-flow"), ExpiresAt: time.Now().Add(api.IssueSlackOAuthFlowTTL)}
		if e := s.StartIssueSlackOAuth(ctx, f); e != nil {
			t.Fatal(e)
		}
		claimed, e := s.ClaimIssueSlackOAuth(ctx, f.Hash, owner.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.SaveIssueSlackOAuth(ctx, claimed, []byte("sealed-pending")); e != nil {
			t.Fatal(e)
		}
		f, e = s.GetIssueSlackOAuth(ctx, f.Hash, owner.ID)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.FinishIssueSlackOAuth(ctx, f, state.IssueSlackInstallation{WorkspaceID: "T0123456789", WorkspaceName: "Routing", SlackAppID: "A0123456789", BotUserID: "U0123456789", TokenSealed: []byte("sealed-token")}, []byte("sealed-signing")); e != nil {
			t.Fatal(e)
		}
	}
	install(0)
	for _, tm := range []api.IssueTeam{team, backup} {
		if e = s.SetIssueSlackOAuthDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: tm.ID, ChannelID: "C0123456789", Enabled: true}, 1); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.SetIssueOwnershipRules(ctx, app.ID, owner.ID, []api.IssueOwnershipRule{{ExceptionType: "RoutingError", AssigneeTeamID: team.ID}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created", "regressed", "frequency_threshold_reached"}, FallbackTeamID: backup.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetIssueApplicationReminderPolicy(ctx, app.ID, owner.ID, state.IssueApplicationReminderPolicy{Enabled: true, IntervalMinutes: 5, RepeatLimit: 3}); e != nil {
		t.Fatal(e)
	}
	revision := int64(0)
	if _, e = s.SetIssueFrequencyAlertPolicy(ctx, app.ID, owner.ID, api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &revision, Rules: []api.IssueFrequencyAlertRule{{Key: "burst", Enabled: true, Environment: "production", MinimumEvents: 2, WindowSeconds: 60, CooldownSeconds: 60}}}); e != nil {
		t.Fatal(e)
	}
	rules := []state.IssueSlackRoutingRule{{Key: "prod-new", Enabled: true, Event: "created", Environment: "production", TeamID: team.ID, ChannelID: "C1111111111"}, {Key: "burst", Enabled: true, Event: "frequency_threshold_reached", FrequencyRuleKey: "burst", ChannelID: "C2222222222"}, {Key: "other-frequency", Enabled: true, Event: "frequency_threshold_reached", ChannelID: "C3333333333"}}
	in := state.PutIssueSlackRoutingPolicy{InstallationRevision: 1, Rules: rules}
	if _, e = s.SetIssueSlackRoutingPolicy(ctx, app.ID, member.ID, in); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("member changed rules", e)
	}
	p, e := s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, in)
	if e != nil || p.Revision != 1 || !p.Active {
		t.Fatal(p, e)
	}
	in.Revision = p.Revision
	preview, e := s.PreviewIssueSlackRouting(ctx, app.ID, owner.ID, in, state.IssueSlackRoutingSample{Event: "frequency_threshold_reached", Environment: "production", FrequencyRuleKey: "burst", AssignedTeamID: team.ID})
	if e != nil || preview.RuleKey != "burst" || preview.ChannelID != "C2222222222" || preview.TeamID != team.ID {
		t.Fatal(preview, e)
	}
	draft := in
	draft.Rules = append([]state.IssueSlackRoutingRule{}, rules...)
	draft.Rules[1], draft.Rules[2] = draft.Rules[2], draft.Rules[1]
	preview, e = s.PreviewIssueSlackRouting(ctx, app.ID, owner.ID, draft, state.IssueSlackRoutingSample{Event: "frequency_threshold_reached", Environment: "production", FrequencyRuleKey: "burst", AssignedTeamID: team.ID})
	if e != nil || preview.RuleKey != "other-frequency" {
		t.Fatal(preview, e)
	}
	p, _ = s.GetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID)
	if p.Rules[1].Key != "burst" || p.Revision != 1 {
		t.Fatal("preview mutated policy", p)
	}
	preview, e = s.PreviewIssueSlackRouting(ctx, app.ID, owner.ID, in, state.IssueSlackRoutingSample{Event: "created", Environment: "staging", AssignedTeamID: team.ID})
	if e != nil || preview.Outcome != "team_default" || preview.ChannelID != "C0123456789" {
		t.Fatal(preview, e)
	}
	record := func(key, env string) string {
		t.Helper()
		r, e := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: env}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "RoutingError", SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: "private-routing-evidence", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if e != nil {
			t.Fatal(e)
		}
		return r.IssueID
	}
	issue := record("a", "production")
	record("a", "production")
	if _, e = s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, other.ID, issue); !errors.Is(e, state.ErrNotFound) {
		t.Fatal("channel override granted unrelated team access", e)
	}
	if _, e = s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, member.ID, issue); e != nil {
		t.Fatal("responsible team lost access", e)
	}
	var started time.Time
	if e = pool.QueryRow(ctx, "SELECT reminder_started_at FROM issue_application_triage WHERE issue_id=$1", issue).Scan(&started); e != nil {
		t.Fatal(e)
	}
	roots := map[string]string{}
	sent := 0
	client := issueslack.NewClient(&http.Client{Transport: slackOAuthTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://slack.com/api/chat.postMessage" {
			t.Fatal("unexpected transport")
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Channel string `json:"channel"`
			Thread  string `json:"thread_ts"`
			ID      string `json:"client_msg_id"`
		}
		if e := json.Unmarshal(raw, &body); e != nil {
			t.Fatal(e)
		}
		if body.Thread != roots[body.Channel] || strings.Contains(string(raw), "private-routing-evidence") {
			t.Fatal("thread crossed channels or leaked evidence", string(raw))
		}
		sent++
		ts := fmt.Sprintf("123.%06d", sent)
		if roots[body.Channel] == "" {
			roots[body.Channel] = ts
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"ok":true,"channel":%q,"ts":%q}`, body.Channel, ts)))}, nil
	})})
	worker := issueslack.Worker{Store: s, Client: client, OpenToken: func([]byte) (string, error) { return "xoxb-safe-token", nil }, DashboardBase: "https://gregale.example"}
	step := func() {
		t.Helper()
		exec("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
		ok, e := worker.Step(ctx)
		if e != nil || !ok {
			t.Fatal(ok, e)
		}
	}
	step()
	step()
	if roots["C1111111111"] == "" || roots["C2222222222"] == "" {
		t.Fatal("missing distinct channel roots", roots)
	}
	exec("UPDATE issue_frequency_alert_receipts SET last_alert_at=clock_timestamp()-interval '2 minutes' WHERE issue_id=$1", issue)
	record("a", "production")
	step()
	rows, e := s.ListIssueSlackDeliveries(ctx, app.ID, owner.ID)
	if e != nil || len(rows) != 3 {
		t.Fatal(rows, e)
	}
	for _, r := range rows {
		if r.RoutingRevision != 1 || r.RoutingRuleKey == "" || r.ChannelID == "C0123456789" {
			t.Fatal("missing routing evidence", r)
		}
	}
	record("b", "staging")
	step()
	if roots["C0123456789"] == "" {
		t.Fatal("default channel missing")
	}
	// Rule changes fence already claimed deliveries and preserve response responsibility.
	exec("UPDATE issue_frequency_alert_receipts SET last_alert_at=clock_timestamp()-interval '2 minutes' WHERE issue_id=$1", issue)
	record("a", "production")
	exec("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
	claim, found, e := s.ClaimIssueSlackDelivery(ctx)
	if e != nil || !found || claim.Status != "in_flight" {
		t.Fatal(claim, found, e)
	}
	changed := in
	changed.Rules = append([]state.IssueSlackRoutingRule{}, rules...)
	changed.Rules[1].ChannelID = "C4444444444"
	p, e = s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, changed)
	if e != nil || p.Revision != 2 {
		t.Fatal(p, e)
	}
	changed.Revision = p.Revision
	if ready, e := s.IssueSlackDeliveryReady(ctx, claim); e != nil || ready {
		t.Fatal("old route claim survived", ready, e)
	}
	if e = s.FinishIssueSlackDelivery(ctx, claim, state.IssueSlackResult{Status: "succeeded", MessageTS: "999.1", ResponseCode: 200}); !errors.Is(e, state.ErrIssueSlackClaimLost) {
		t.Fatal("old claim completed", e)
	}
	if e = s.RetryIssueSlackDelivery(ctx, app.ID, owner.ID, claim.ID); e == nil {
		t.Fatal("old route retry admitted")
	}
	var stillStarted time.Time
	if e = pool.QueryRow(ctx, "SELECT reminder_started_at FROM issue_application_triage WHERE issue_id=$1", issue).Scan(&stillStarted); e != nil || !stillStarted.Equal(started) {
		t.Fatal("routing reset reminder clock", stillStarted, e)
	}
	exec("UPDATE issue_frequency_alert_receipts SET last_alert_at=clock_timestamp()-interval '2 minutes' WHERE issue_id=$1", issue)
	record("a", "production")
	p, e = s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, changed)
	if e != nil || p.Revision != 2 {
		t.Fatal("no-op advanced revision", p, e)
	}
	step()
	if roots["C4444444444"] == "" {
		t.Fatal("replacement channel did not start a thread")
	}
	if _, e = s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, in); e == nil {
		t.Fatal("stale routing save")
	}
	// A competing rule update has exactly one winner.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for n := range 2 {
		candidate := changed
		candidate.Rules = append([]state.IssueSlackRoutingRule{}, changed.Rules...)
		candidate.Rules[0].Key = fmt.Sprintf("candidate-%d", n)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, candidate)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("competing update", success)
	}
	p, _ = s.GetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID)
	// Owner and installation binding require an explicit reviewed save.
	exec("UPDATE issue_slack_routing_policies SET account_id=$2 WHERE app_id=$1", app.ID, other.ID)
	if bound, _ := s.GetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID); bound.Active {
		t.Fatal("previous owner rules active")
	}
	p, e = s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, state.PutIssueSlackRoutingPolicy{Revision: p.Revision, InstallationRevision: 1, Rules: p.Rules})
	if e != nil || !p.Active {
		t.Fatal("owner rebinding", p, e)
	}
	before := p.Revision
	install(1)
	p, _ = s.GetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID)
	if p.Active || p.Revision != before {
		t.Fatal("reconnect silently activated routes", p)
	}
	dests, _ := s.ListIssueSlackDestinations(ctx, app.ID, owner.ID)
	for _, d := range dests {
		if d.TeamID == team.ID {
			if e = s.SetIssueSlackOAuthDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C0123456789", Enabled: true, Revision: d.Revision}, 2); e != nil {
				t.Fatal(e)
			}
		}
	}
	record("c", "production")
	rows, _ = s.ListIssueSlackDeliveries(ctx, app.ID, owner.ID)
	if rows[0].RoutingRuleKey != "" || rows[0].ChannelID != "C0123456789" {
		t.Fatal("inactive routes selected old workspace channel", rows[0])
	}
	if _, e = s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, state.PutIssueSlackRoutingPolicy{Revision: p.Revision, InstallationRevision: 1, Rules: p.Rules}); e == nil {
		t.Fatal("old installation saved routes")
	}
	p, e = s.SetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID, state.PutIssueSlackRoutingPolicy{Revision: p.Revision, InstallationRevision: 2, Rules: p.Rules})
	if e != nil || !p.Active {
		t.Fatal(p, e)
	}
	if e = s.DisconnectIssueSlackOAuth(ctx, app.ID, owner.ID, 2); e != nil {
		t.Fatal(e)
	}
	p, _ = s.GetIssueSlackRoutingPolicy(ctx, app.ID, owner.ID)
	if p.Active {
		t.Fatal("disconnected routing active")
	}
}
