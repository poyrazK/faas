package state_test

import (
	"context"
	"crypto/sha256"
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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

type slackAssignmentFixture struct {
	s             *state.PgStore
	pool          *pgxpool.Pool
	ctx           context.Context
	owner, member state.Account
	app           state.App
	dep           state.Deployment
	org, team     string
	policy        state.IssueSlackInteractionPolicy
	worker        issueslack.Worker
	seq           int
	ts            map[string]string
	canAssign     map[string]bool
	lastID        string
}

func newSlackAssignmentFixture(t *testing.T) *slackAssignmentFixture {
	t.Helper()
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	h := &slackAssignmentFixture{s: s, pool: pool, ctx: ctx, owner: owner, app: app, dep: seedPgDeployment(t, s, ctx, app), ts: map[string]string{}, canAssign: map[string]bool{}}
	org, err := s.CreateOrg(ctx, state.Org{Slug: "assign-" + uuid.NewString()[:8], Name: "Responders", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	h.org = org.ID
	h.member, err = s.CreateAccount(ctx, "assign-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, h.member.ID} {
		if err = s.AddOrgMember(ctx, org.ID, id, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
	}
	h.exec(t, "UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID)
	team, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{owner.ID, h.member.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h.team = team.ID
	if _, err = s.SetIssueOwnershipRules(ctx, app.ID, owner.ID, []api.IssueOwnershipRule{{ExceptionType: "AssignError", AssigneeTeamID: team.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueSlackDestination(ctx, app.ID, owner.ID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C0123456789", Enabled: true, TokenSealed: []byte("test-sealed")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: team.ID}); err != nil {
		t.Fatal(err)
	}
	h.policy, err = s.SetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID, state.IssueSlackInteractionPolicy{Enabled: true, WorkspaceID: "T0123456789", SlackAppID: "A0123456789", SecretSealed: []byte("test-secret-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAppWebhook(ctx, state.AppWebhook{AccountID: owner.ID, AppID: app.ID, Enabled: true, TargetURL: "https://example.com/audit", EventFilter: []string{"issue.assigned"}, SecretSealed: []byte("test-sealed")}); err != nil {
		t.Fatal(err)
	}
	h.worker = issueslack.Worker{Store: s, OpenToken: func([]byte) (string, error) { return "xoxb-test", nil }, DashboardBase: "https://gregale.dev", Client: issueslack.NewClient(&http.Client{Transport: slackActionTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://slack.com/api/chat.postMessage" {
			return nil, errors.New("unexpected Slack destination")
		}
		var body struct {
			ID string `json:"client_msg_id"`
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		h.canAssign[body.ID] = strings.Contains(string(raw), `"gregale_assign_to_me"`)
		h.lastID = body.ID
		ts := fmt.Sprintf("123.%06d", len(h.ts)+1)
		h.ts[body.ID] = ts
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"ok":true,"channel":"C0123456789","ts":%q}`, ts)))}, nil
	})})}
	return h
}

func (h *slackAssignmentFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := h.pool.Exec(h.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func (h *slackAssignmentFixture) fresh(in state.IssueSlackInteractionRequest) state.IssueSlackInteractionRequest {
	h.seq++
	in.RequestKey = fmt.Sprintf("%064x", h.seq)
	return in
}

func (h *slackAssignmentFixture) send(t *testing.T, key string) (string, state.IssueSlackInteractionRequest) {
	t.Helper()
	now := time.Now().UTC()
	r, err := h.s.RecordIssue(h.ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: h.owner.ID, AppID: h.app.ID, DeploymentID: h.dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "AssignError", SourceKind: "exception"}, Fingerprint: fmt.Sprintf("%x", sha256.Sum256([]byte(key))), Title: "private evidence", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h.exec(t, "UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", h.app.ID)
	h.lastID = ""
	if ok, err := h.worker.Step(h.ctx); err != nil || !ok {
		t.Fatal("send", ok, err)
	}
	var sentIssue string
	err = h.pool.QueryRow(h.ctx, `SELECT act.issue_id::text FROM issue_team_slack_deliveries d
 JOIN issue_activity act ON act.id=d.activity_id WHERE d.id=$1 AND d.status='succeeded'`, h.lastID).Scan(&sentIssue)
	if err != nil || sentIssue != r.IssueID || !h.canAssign[h.lastID] {
		t.Fatal("missing matching interactive delivery", sentIssue, err)
	}
	return r.IssueID, h.fresh(state.IssueSlackInteractionRequest{DeliveryID: h.lastID, ChannelID: "C0123456789", MessageTS: h.ts[h.lastID], UserID: "U0123456789", Action: "assign_to_me", PolicyRevision: h.policy.Revision})
}

func (h *slackAssignmentFixture) link(t *testing.T, issue, actor string, in state.IssueSlackInteractionRequest) {
	t.Helper()
	r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in)
	if err != nil || r.Status != "link_required" {
		t.Fatal("link challenge", r, err)
	}
	if err = h.s.ConfirmIssueSlackAccountLink(h.ctx, h.app.ID, h.owner.ID, actor, issue, r.LinkToken); err != nil {
		t.Fatal(err)
	}
	if r, err = h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err != nil || r.Status != "linked" {
		t.Fatal("original linking click executed", r, err)
	}
}

func (h *slackAssignmentFixture) detail(t *testing.T, issue string) api.IssueDetail {
	t.Helper()
	d, err := h.s.GetIssueDetail(h.ctx, h.app.ID, issue, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), state.IssueDetailCursors{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func (h *slackAssignmentFixture) slackAssignments(t *testing.T, issue string, want int) {
	t.Helper()
	var activity, outbox int
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM issue_activity WHERE issue_id=$1 AND action='assigned' AND details->>'assignment_source'='slack'`, issue).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(h.ctx, `SELECT count(*) FROM app_webhook_event_outbox WHERE app_id=$1 AND event='issue.assigned' AND payload->'issue'->>'id'=$2 AND payload->'details'->>'assignment_source'='slack'`, h.app.ID, issue).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if activity != want || outbox != want {
		t.Fatalf("assignment activity/outbox=%d/%d want=%d", activity, outbox, want)
	}
}

func TestPgSlackSelfAssignment(t *testing.T) {
	h := newSlackAssignmentFixture(t)
	issue, in := h.send(t, "member-link")
	h.link(t, issue, h.member.ID, in)
	issue, in = h.send(t, "owner-link")
	in.UserID = "U9876543210"
	h.link(t, issue, h.owner.ID, in)

	t.Run("assignment_audit_outbox_and_response_isolation", func(t *testing.T) {
		issue, in := h.send(t, t.Name())
		r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in)
		if err != nil || r.Status != "assigned" {
			t.Fatal(r, err)
		}
		d := h.detail(t, issue)
		if d.Issue.AssigneeAccountID != h.member.ID || d.Issue.AssigneeTeamID != "" || d.Issue.State != "open" {
			t.Fatal(d.Issue)
		}
		for _, a := range d.Activity {
			if a.Details["assignment_source"] == "slack" && (a.ActorAccountID != h.member.ID || a.Details["assignee_account_id"] != h.member.ID) {
				t.Fatal("assignment actor", a)
			}
		}
		triage, err := h.s.GetIssueApplicationTriage(h.ctx, h.app.ID, h.owner.ID, h.member.ID, issue)
		if err != nil || triage.Revision != 0 || triage.AcknowledgedAt != nil || triage.SnoozedUntil != nil {
			t.Fatal("assignment changed response", triage, err)
		}
		for range 2 {
			if _, err = h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, h.fresh(in)); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("old ownership snapshot", err)
			}
		}
		h.slackAssignments(t, issue, 1)
	})
	t.Run("legacy_message_and_link_require_separate_consent", func(t *testing.T) {
		issue, in := h.send(t, t.Name()+"-old-message")
		h.exec(t, "UPDATE issue_slack_action_messages SET can_assign=false WHERE delivery_id=$1", in.DeliveryID)
		// A retry after a binary upgrade retains the prepared snapshot's old
		// capabilities rather than advertising an unusable assignment action.
		h.exec(t, "UPDATE issue_team_slack_deliveries SET status='failed',next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1", in.DeliveryID)
		h.exec(t, "UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", h.app.ID)
		if ok, err := h.worker.Step(h.ctx); err != nil || !ok || h.canAssign[in.DeliveryID] {
			t.Fatal("legacy retry upgraded buttons", ok, err)
		}
		in.MessageTS = h.ts[in.DeliveryID]
		if _, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("legacy message acquired assignment", err)
		}
		in.Action = "acknowledge"
		if r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, h.fresh(in)); err != nil || r.Status != "handled" {
			t.Fatal("legacy acknowledgment", r, err)
		}
		h.slackAssignments(t, issue, 0)
		h.exec(t, "UPDATE issue_slack_identities SET can_assign=false WHERE app_id=$1 AND account_id=$2", h.app.ID, h.member.ID)
		issue, in = h.send(t, t.Name()+"-old-link")
		r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in)
		if err != nil || r.Status != "link_required" || h.detail(t, issue).Issue.AssigneeTeamID != h.team {
			t.Fatal("legacy link acquired assignment", r, err)
		}
		retry, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in)
		if err != nil || retry.LinkToken != r.LinkToken {
			t.Fatal("unstable upgrade challenge", retry, err)
		}
		if err = h.s.ConfirmIssueSlackAccountLink(h.ctx, h.app.ID, h.owner.ID, h.member.ID, issue, r.LinkToken); err != nil {
			t.Fatal(err)
		}
		if r, err = h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err != nil || r.Status != "linked" {
			t.Fatal("upgrade confirmation performed action", r, err)
		}
		h.slackAssignments(t, issue, 0)
		if r, err = h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, h.fresh(in)); err != nil || r.Status != "assigned" {
			t.Fatal("confirmed upgrade", r, err)
		}
		h.slackAssignments(t, issue, 1)
	})
	t.Run("membership_is_checked_again", func(t *testing.T) {
		issue, in := h.send(t, t.Name())
		h.exec(t, "UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", h.org, h.member.ID)
		t.Cleanup(func() {
			h.exec(t, "UPDATE org_memberships SET removed_at=NULL WHERE org_id=$1 AND account_id=$2", h.org, h.member.ID)
		})
		if _, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("removed member assigned", err)
		}
		if h.detail(t, issue).Issue.AssigneeTeamID != h.team {
			t.Fatal("unauthorized ownership change")
		}
		h.slackAssignments(t, issue, 0)
	})
	t.Run("already_assigned_to_actor_is_a_noop", func(t *testing.T) {
		if _, err := h.s.SetIssueOwnershipRules(h.ctx, h.app.ID, h.owner.ID, []api.IssueOwnershipRule{{ExceptionType: "AssignError", AssigneeAccountID: h.member.ID}}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := h.s.SetIssueOwnershipRules(h.ctx, h.app.ID, h.owner.ID, []api.IssueOwnershipRule{{ExceptionType: "AssignError", AssigneeTeamID: h.team}}); err != nil {
				t.Fatal(err)
			}
		})
		issue, in := h.send(t, t.Name())
		if r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err != nil || r.Status != "assigned" {
			t.Fatal("self-owned issue", r, err)
		}
		if r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err != nil || r.Status != "already_handled" {
			t.Fatal("noop message was not consumed", r, err)
		}
		h.slackAssignments(t, issue, 0)
	})
	t.Run("ownership_away_and_back_does_not_revive_buttons", func(t *testing.T) {
		issue, in := h.send(t, t.Name())
		for _, req := range []api.IssueActionRequest{{Action: "assign", AssigneeAccountID: h.owner.ID}, {Action: "assign", AssigneeTeamID: h.team}} {
			if _, err := h.s.ActOnIssue(h.ctx, h.app.ID, issue, h.owner.ID, req, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err != nil || r.Status != "already_handled" {
			t.Fatal("old ownership revived", r, err)
		}
		if h.detail(t, issue).Issue.AssigneeTeamID != h.team {
			t.Fatal("old message overwrote ownership")
		}
		h.slackAssignments(t, issue, 0)
	})
	t.Run("merge_and_split_do_not_revive_canonical_buttons", func(t *testing.T) {
		root, in := h.send(t, t.Name()+"-root")
		member, _ := h.send(t, t.Name()+"-member")
		if _, err := h.s.ActOnIssue(h.ctx, h.app.ID, member, h.owner.ID, api.IssueActionRequest{Action: "merge", TargetIssueID: root}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.s.ActOnIssue(h.ctx, h.app.ID, root, h.owner.ID, api.IssueActionRequest{Action: "split", MemberIssueID: member}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err != nil || r.Status != "already_handled" {
			t.Fatal("old grouping revived", r, err)
		}
		h.slackAssignments(t, root, 0)
	})
	t.Run("failed_outbox_rolls_back_ownership_activity_and_consumption", func(t *testing.T) {
		issue, in := h.send(t, t.Name())
		h.exec(t, "CREATE FUNCTION reject_slack_assignment() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$")
		h.exec(t, "CREATE TRIGGER reject_slack_assignment BEFORE INSERT ON app_webhook_event_outbox FOR EACH ROW WHEN (NEW.event='issue.assigned') EXECUTE FUNCTION reject_slack_assignment()")
		if _, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err == nil {
			t.Fatal("injected outbox failure ignored")
		}
		if h.detail(t, issue).Issue.AssigneeTeamID != h.team {
			t.Fatal("partial ownership commit")
		}
		h.slackAssignments(t, issue, 0)
		h.exec(t, "DROP TRIGGER reject_slack_assignment ON app_webhook_event_outbox")
		h.exec(t, "DROP FUNCTION reject_slack_assignment()")
		if r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, in); err != nil || r.Status != "assigned" {
			t.Fatal("retry after rollback", r, err)
		}
		h.slackAssignments(t, issue, 1)
	})
	t.Run("competing_responders_assign_once", func(t *testing.T) {
		issue, in := h.send(t, t.Name())
		other := h.fresh(in)
		other.UserID = "U9876543210"
		var wg sync.WaitGroup
		results := make(chan string, 2)
		for _, req := range []state.IssueSlackInteractionRequest{in, other} {
			wg.Go(func() {
				r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, req)
				if err != nil && !errors.Is(err, state.ErrNotFound) {
					results <- "unexpected_error"
					return
				}
				results <- r.Status
			})
		}
		wg.Wait()
		close(results)
		winners := 0
		for status := range results {
			if status == "assigned" {
				winners++
			} else if status != "" && status != "already_handled" {
				t.Fatal("competing callback", status)
			}
		}
		if winners != 1 {
			t.Fatal("assignment winners", winners)
		}
		h.slackAssignments(t, issue, 1)
	})
	t.Run("existing_consent_can_be_renewed_at_identity_capacity", func(t *testing.T) {
		var count int
		if err := h.pool.QueryRow(h.ctx, "SELECT count(*) FROM issue_slack_identities WHERE app_id=$1", h.app.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		for i := count; i < api.IssueSlackIdentitiesMax; i++ {
			account, err := h.s.CreateAccount(h.ctx, "capacity-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			h.exec(t, `INSERT INTO issue_slack_identities(app_id,slack_user_id,account_id,policy_revision,mfa_required,expires_at)
 VALUES($1,$2,$3,$4,false,clock_timestamp()+interval '1 day')`, h.app.ID, fmt.Sprintf("U%010d", i), account.ID, h.policy.Revision)
		}
		h.exec(t, "UPDATE issue_slack_identities SET can_assign=false WHERE app_id=$1 AND account_id=$2", h.app.ID, h.member.ID)
		issue, in := h.send(t, t.Name())
		h.link(t, issue, h.member.ID, in)
		if r, err := h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, h.fresh(in)); err != nil || r.Status != "assigned" {
			t.Fatal("consent renewal at capacity", r, err)
		}
	})
}

func TestPgSlackSelfAssignmentMigrationReplay(t *testing.T) {
	h := newSlackAssignmentFixture(t)
	issue, in := h.send(t, "migration")
	h.link(t, issue, h.member.ID, in)
	raw, err := migrations.FS.ReadFile("20261017211827747_issue_slack_self_assignment.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing rollback")
	}
	for range 2 {
		h.exec(t, parts[0])
	}
	var canAssign bool
	if err = h.pool.QueryRow(h.ctx, "SELECT can_assign FROM issue_slack_identities WHERE app_id=$1 AND account_id=$2", h.app.ID, h.member.ID).Scan(&canAssign); err != nil || !canAssign {
		t.Fatal("replay changed consent", canAssign, err)
	}
	h.exec(t, parts[1])
	for range 2 {
		h.exec(t, parts[0])
	}
	var message, identity bool
	if err = h.pool.QueryRow(h.ctx, "SELECT m.can_assign,l.can_assign FROM issue_slack_action_messages m JOIN issue_slack_identities l ON l.app_id=$2 AND l.account_id=$3 WHERE m.delivery_id=$1", in.DeliveryID, h.app.ID, h.member.ID).Scan(&message, &identity); err != nil || message || identity {
		t.Fatal("upgrade reconstructed authority", message, identity, err)
	}
	if _, err = h.s.ApplyIssueSlackInteraction(h.ctx, h.app.ID, h.fresh(in)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("old message acquired assignment", err)
	}
	if h.detail(t, issue).Issue.AssigneeTeamID != h.team {
		t.Fatal("migration changed ownership")
	}
}
