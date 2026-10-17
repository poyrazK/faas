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

type slackActionTransport func(*http.Request) (*http.Response, error)

func (f slackActionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPg_NativeSlackActionsAndAccountLinks(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	org, err := s.CreateOrg(ctx, state.Org{Slug: "actions-" + uuid.NewString()[:8], Name: "Actions", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateAccount(ctx, "action-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := s.CreateAccount(ctx, "other-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, member.ID} {
		if err = s.AddOrgMember(ctx, org.ID, id, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID)
	team, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{member.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetIssueSlackDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: team.ID, Enabled: true, ChannelID: "C0123456789", TokenSealed: []byte("fake-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetIssueOwnershipRules(ctx, app.ID, owner.ID, []api.IssueOwnershipRule{{ExceptionType: "InteractionError", AssigneeTeamID: team.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created", "regressed"}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.GetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID)
	if err != nil || policy.Enabled || policy.Revision != 0 {
		t.Fatal(policy, err)
	}
	policy = state.IssueSlackInteractionPolicy{Enabled: true, WorkspaceID: "T0123456789", SlackAppID: "A0123456789", SecretSealed: []byte("fake-secret-sealed")}
	if _, err = s.SetIssueSlackInteractionPolicy(ctx, app.ID, member.ID, policy); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member changed policy", err)
	}
	policy, err = s.SetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID, policy)
	if err != nil || policy.Revision != 1 || len(policy.SecretSealed) != 0 {
		t.Fatal(policy, err)
	}
	if _, err = s.SetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID, state.IssueSlackInteractionPolicy{Enabled: true, Revision: 0, WorkspaceID: policy.WorkspaceID, SlackAppID: policy.SlackAppID}); err == nil {
		t.Fatal("stale save")
	}
	same, err := s.SetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID, policy)
	if err != nil || same.Revision != 1 {
		t.Fatal("no-op", same, err)
	}
	tsByID := map[string]string{}
	sent := 0
	client := issueslack.NewClient(&http.Client{Transport: slackActionTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://slack.com/api/chat.postMessage" {
			t.Fatal("unexpected network destination")
		}
		raw, _ := io.ReadAll(r.Body)
		var p map[string]any
		if json.Unmarshal(raw, &p) != nil || strings.Contains(string(raw), "private-event") || !strings.Contains(string(raw), "gregale_acknowledge") {
			t.Fatal(string(raw))
		}
		sent++
		ts := fmt.Sprintf("123.%06d", sent)
		tsByID[p["client_msg_id"].(string)] = ts
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"ok":true,"channel":"C0123456789","ts":%q}`, ts)))}, nil
	})})
	worker := issueslack.Worker{Store: s, Client: client, OpenToken: func([]byte) (string, error) { return "xoxb-test", nil }, DashboardBase: "https://gregale.dev"}
	seq := 0
	send := func(key string) (string, state.IssueSlackInteractionRequest) {
		t.Helper()
		r, e := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "InteractionError", SourceKind: "exception"}, Fingerprint: strings.Repeat(key, 64), Title: "private-event", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if e != nil {
			t.Fatal(e)
		}
		exec("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
		if ok, e := worker.Step(ctx); e != nil || !ok {
			t.Fatal(ok, e)
		}
		rows, e := s.ListIssueSlackDeliveries(ctx, app.ID, owner.ID)
		if e != nil {
			t.Fatal(e)
		}
		for _, d := range rows {
			if d.IssueID == r.IssueID && d.Status == "succeeded" {
				seq++
				return r.IssueID, state.IssueSlackInteractionRequest{DeliveryID: d.ID, ChannelID: d.ChannelID, MessageTS: tsByID[d.ID], UserID: "U0123456789", Action: "acknowledge", PolicyRevision: policy.Revision, RequestKey: fmt.Sprintf("%064x", seq)}
			}
		}
		t.Fatal("missing sent delivery")
		return "", state.IssueSlackInteractionRequest{}
	}
	fresh := func(in state.IssueSlackInteractionRequest) state.IssueSlackInteractionRequest {
		seq++
		in.RequestKey = fmt.Sprintf("%064x", seq)
		return in
	}
	get := func(id string) state.IssueApplicationTriage {
		t.Helper()
		v, e := s.GetIssueApplicationTriage(ctx, app.ID, owner.ID, owner.ID, id)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	issue, in := send("a")
	bad := in
	bad.MessageTS = "999.999"
	if _, err = s.ApplyIssueSlackInteraction(ctx, app.ID, bad); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("message context", err)
	}
	result, err := s.ApplyIssueSlackInteraction(ctx, app.ID, in)
	if err != nil || result.Status != "link_required" || len(result.LinkToken) != 64 {
		t.Fatal(result, err)
	}
	retry, err := s.ApplyIssueSlackInteraction(ctx, app.ID, in)
	if err != nil || retry.LinkToken != result.LinkToken {
		t.Fatal("link retry", retry, err)
	}
	if get(issue).AcknowledgedAt != nil {
		t.Fatal("unlinked click mutated response")
	}
	if _, err = s.GetIssueSlackAccountLink(ctx, app.ID, owner.ID, outsider.ID, issue, result.LinkToken); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unauthorized link read", err)
	}
	if err = s.ConfirmIssueSlackAccountLink(ctx, app.ID, owner.ID, outsider.ID, issue, result.LinkToken); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unauthorized link confirmation", err)
	}
	if err = s.ConfirmIssueSlackAccountLink(ctx, app.ID, owner.ID, member.ID, issue, result.LinkToken); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmIssueSlackAccountLink(ctx, app.ID, owner.ID, member.ID, issue, result.LinkToken); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("challenge reused", err)
	}
	retry, err = s.ApplyIssueSlackInteraction(ctx, app.ID, in)
	if err != nil || retry.Status != "linked" || get(issue).AcknowledgedAt != nil {
		t.Fatal("old unlinked request applied after linking", retry, err)
	}
	in = fresh(in)
	result, err = s.ApplyIssueSlackInteraction(ctx, app.ID, in)
	if err != nil || result.Status != "handled" {
		t.Fatal(result, err)
	}
	v := get(issue)
	if v.AcknowledgedBy != member.ID || v.Revision != 1 || len(v.History) != 1 {
		t.Fatal("shared audit", v)
	}
	if _, err = s.ApplyIssueSlackInteraction(ctx, app.ID, in); err != nil || get(issue).Revision != 1 {
		t.Fatal("duplicate action", err)
	}
	issueB, inB := send("b")
	inB.Action = "snooze_1h"
	result, err = s.ApplyIssueSlackInteraction(ctx, app.ID, inB)
	if err != nil || result.Status != "handled" {
		t.Fatal(result, err)
	}
	v = get(issueB)
	if !v.Snoozed || v.SnoozedBy != member.ID || v.SnoozedUntil.Before(time.Now().Add(59*time.Minute)) || v.SnoozedUntil.After(time.Now().Add(61*time.Minute)) {
		t.Fatal("snooze", v)
	}
	until := *v.SnoozedUntil
	inB.Action = "snooze_24h"
	_, _ = s.ApplyIssueSlackInteraction(ctx, app.ID, fresh(inB))
	if !get(issueB).SnoozedUntil.Equal(until) {
		t.Fatal("replay extended snooze")
	}
	issueC, inC := send("c")
	if err = s.UpdateIssueApplicationTriage(ctx, app.ID, owner.ID, owner.ID, issueC, 0, state.IssueApplicationTriageAction{Action: "acknowledge"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyIssueSlackInteraction(ctx, app.ID, inC); err == nil {
		t.Fatal("stale response action")
	}
	issueD, inD := send("d")
	exec("UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	if _, err = s.ApplyIssueSlackInteraction(ctx, app.ID, inD); !errors.Is(err, state.ErrNotFound) || get(issueD).AcknowledgedAt != nil {
		t.Fatal("removed member acted", err)
	}
	exec("UPDATE org_memberships SET removed_at=NULL WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	// SQL failure rolls back handling, audit and message consumption together.
	exec("CREATE FUNCTION reject_slack_action() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected'; END $$")
	exec("CREATE TRIGGER reject_slack_action BEFORE INSERT ON issue_application_triage_activity FOR EACH ROW EXECUTE FUNCTION reject_slack_action()")
	if _, err = s.ApplyIssueSlackInteraction(ctx, app.ID, inD); err == nil || get(issueD).AcknowledgedAt != nil {
		t.Fatal("partial action transaction", err)
	}
	exec("DROP TRIGGER reject_slack_action ON issue_application_triage_activity")
	exec("DROP FUNCTION reject_slack_action()")
	result, err = s.ApplyIssueSlackInteraction(ctx, app.ID, inD)
	if err != nil || result.Status != "handled" {
		t.Fatal("retry rolled-back action", result, err)
	}
	// Competing clicks mutate once, regardless of callback arrival order.
	issueE, inE := send("e")
	var wg sync.WaitGroup
	statuses := make(chan string, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.ApplyIssueSlackInteraction(ctx, app.ID, inE)
			if e != nil {
				statuses <- "error"
			} else {
				statuses <- r.Status
			}
		}()
	}
	wg.Wait()
	close(statuses)
	handled := 0
	for status := range statuses {
		if status == "handled" {
			handled++
		}
	}
	if handled != 1 || get(issueE).Revision != 1 {
		t.Fatal("concurrent repeat", handled)
	}
	// A linked Gregale account cannot silently acquire a second Slack identity.
	issueF, inF := send("f")
	inF.UserID = "U9876543210"
	result, err = s.ApplyIssueSlackInteraction(ctx, app.ID, inF)
	if err != nil || result.Status != "link_required" {
		t.Fatal(result, err)
	}
	if err = s.ConfirmIssueSlackAccountLink(ctx, app.ID, owner.ID, member.ID, issueF, result.LinkToken); err == nil {
		t.Fatal("identity overwritten")
	}
	if err = s.DeleteIssueSlackAccountLink(ctx, app.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmIssueSlackAccountLink(ctx, app.ID, owner.ID, member.ID, issueF, result.LinkToken); err != nil {
		t.Fatal("self unlink", err)
	}
	// MFA changes require a fresh browser-confirmed link.
	exec("UPDATE accounts SET mfa_required=true WHERE id=$1", member.ID)
	req := fresh(inF)
	result, err = s.ApplyIssueSlackInteraction(ctx, app.ID, req)
	if err != nil || result.Status != "link_required" || get(issueF).AcknowledgedAt != nil {
		t.Fatal("MFA change", result, err)
	}
	exec("UPDATE accounts SET mfa_required=false WHERE id=$1", member.ID)
	if link, e := s.GetIssueSlackAccountLink(ctx, app.ID, owner.ID, member.ID, issueF, ""); e != nil || link.UserID != "" {
		t.Fatal("MFA link revived", link, e)
	}
	// Revocation preserves stale message snapshots; they cannot be reissued for a new policy.
	policy.SecretSealed = []byte("rotated-secret")
	policy, err = s.SetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID, policy)
	if err != nil {
		t.Fatal(err)
	}
	inF.PolicyRevision = policy.Revision
	if _, err = s.ApplyIssueSlackInteraction(ctx, app.ID, fresh(inF)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("old button after rotation", err)
	}
	if link, err := s.GetIssueSlackAccountLink(ctx, app.ID, owner.ID, member.ID, issueF, ""); err != nil || link.UserID != "" {
		t.Fatal("rotation retained identity", link, err)
	}
	issueG, inG := send("1")
	exec(`INSERT INTO issue_slack_link_challenges(token,app_id,delivery_id,request_key,slack_user_id,policy_revision,expires_at)
 SELECT md5('token'||n)||md5('token-extra'||n),$1,$2,md5('key'||n)||md5('key-extra'||n),'U0000000000',$3,clock_timestamp()+interval '10 minutes'
 FROM generate_series(1,$4::integer-(SELECT count(*)::integer FROM issue_slack_link_challenges WHERE app_id=$1)) n`, app.ID, inG.DeliveryID, policy.Revision, api.IssueSlackLinkChallengesMax)
	if _, err = s.ApplyIssueSlackInteraction(ctx, app.ID, inG); err == nil || get(issueG).AcknowledgedAt != nil {
		t.Fatal("challenge capacity bypassed")
	}
	exec("UPDATE issue_slack_link_challenges SET expires_at=clock_timestamp()-interval '1 minute' WHERE app_id=$1", app.ID)
	result, err = s.ApplyIssueSlackInteraction(ctx, app.ID, fresh(inG))
	if err != nil || result.Status != "link_required" {
		t.Fatal("challenge expiry cleanup", result, err)
	}

}
