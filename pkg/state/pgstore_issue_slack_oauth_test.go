package state_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

type slackOAuthTransport func(*http.Request) (*http.Response, error)

func (f slackOAuthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPg_SlackOAuthInstallationLifecycle(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	owner, app := seedPgAccountAndApp(t, s, ctx)
	other, app2 := seedPgAccountAndApp(t, s, ctx)
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	cfg := issueslack.OAuthConfig{ClientID: "123.456", ClientSecret: strings.Repeat("c", 32), RedirectURI: "https://gregale.example/oauth/slack/callback", SlackAppID: "A0123456789", SigningSecret: strings.Repeat("a", 32)}
	exchanges := 0
	client := issueslack.NewClient(&http.Client{Transport: slackOAuthTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/api/oauth.v2.access":
			exchanges++
			body = `{"ok":true,"access_token":"xoxb-private-token","token_type":"bot","app_id":"A0123456789","bot_user_id":"U0123456789","scope":"chat:write,channels:read,groups:read","team":{"id":"T0123456789","name":"Workspace"}}`
		case "/api/auth.test":
			body = `{"ok":true,"team_id":"T0123456789","user_id":"U0123456789","bot_id":"B0123456789"}`
		case "/api/conversations.info":
			body = `{"ok":true,"channel":{"id":"C0123456789","is_member":true}}`
		default:
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	service := issueslack.OAuthService{Store: s, Client: client, Config: cfg, Recipient: id.Recipient(), Identities: []*age.X25519Identity{id}}
	start := func(a state.App, actor string, rev int64) string {
		t.Helper()
		nonce, _, e := service.Start(ctx, a.ID, actor, rev)
		if e != nil {
			t.Fatal(e)
		}
		return nonce
	}
	exchange := func(nonce, actor string) {
		t.Helper()
		if e := service.Exchange(ctx, nonce, actor, "code"); e != nil {
			t.Fatal(e)
		}
	}
	install := func(a state.App, actor string, rev int64) string {
		t.Helper()
		n := start(a, actor, rev)
		exchange(n, actor)
		if e := service.Finish(ctx, n, a.ID, actor); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if _, _, err = service.Start(ctx, app.ID, other.ID, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("nonowner", err)
	}
	nonce := start(app, owner.ID, 0)
	flow, err := s.GetIssueSlackOAuth(ctx, issueslack.OAuthStateHash(nonce), owner.ID)
	if err != nil || strings.Contains(string(flow.Sealed), nonce) {
		t.Fatal("flow sealing", err)
	}
	if err = service.Exchange(ctx, nonce, other.ID, "code"); err == nil || exchanges != 0 {
		t.Fatal("cross account", err)
	}
	exchange(nonce, owner.ID)
	if err = service.Exchange(ctx, nonce, owner.ID, "code"); err == nil || exchanges != 1 {
		t.Fatal("exchange replay", err)
	}
	if err = service.Finish(ctx, nonce, app2.ID, owner.ID); err == nil {
		t.Fatal("cross app confirmation")
	}
	if row, _ := s.GetIssueSlackInstallation(ctx, app.ID, owner.ID); row.Status != "" {
		t.Fatal("GET/exchange installed", row)
	}
	if err = service.Finish(ctx, nonce, app.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err = service.Finish(ctx, nonce, app.ID, owner.ID); err == nil {
		t.Fatal("reused confirmation")
	}
	row, err := s.GetIssueSlackInstallation(ctx, app.ID, owner.ID)
	if err != nil || row.Revision != 1 || row.Status != "connected" || len(row.TokenSealed) != 0 {
		t.Fatal(row, err)
	}
	credential, err := s.IssueSlackInstallationCredential(ctx, app.ID, owner.ID)
	if err != nil || strings.Contains(string(credential.TokenSealed), "xoxb-private-token") {
		t.Fatal("unsealed token", err)
	}
	token, err := issueslack.OpenToken([]*age.X25519Identity{id}, credential.TokenSealed)
	if err != nil || token != "xoxb-private-token" {
		t.Fatal("credential", err)
	}
	encoded, _ := json.Marshal(credential)
	if strings.Contains(string(encoded), "TokenSealed") || strings.Contains(string(encoded), "xoxb-") {
		t.Fatal("serialized credentials")
	}
	team, err := s.PutIssueTeam(ctx, app.ID, owner.ID, owner.ID, "oauth", api.PutIssueTeamRequest{Name: "OAuth", MemberAccountIDs: []string{owner.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	destination := state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C0123456789", Enabled: true}
	if err = service.SetDestination(ctx, app.ID, owner.ID, destination, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueSlackDestination(ctx, app.ID, owner.ID, destination); err == nil {
		t.Fatal("manual token override while connected")
	}
	policy, err := s.GetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID)
	if err != nil || !policy.Enabled || policy.WorkspaceID != row.WorkspaceID {
		t.Fatal(policy, err)
	}
	if _, err = s.SetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID, policy); err == nil {
		t.Fatal("managed policy changed manually")
	}
	dests, err := s.ListIssueSlackDestinations(ctx, app.ID, owner.ID)
	if err != nil || len(dests) != 1 || dests[0].OAuthRevision != 1 {
		t.Fatal(dests, err)
	}
	// A disconnect wins against a callback that already exchanged its code.
	pending := start(app, owner.ID, 1)
	exchange(pending, owner.ID)
	if err = s.DisconnectIssueSlackOAuth(ctx, app.ID, other.ID, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("nonowner disconnect", err)
	}
	if err = s.DisconnectIssueSlackOAuth(ctx, app.ID, owner.ID, 0); err == nil {
		t.Fatal("stale disconnect")
	}
	if err = s.DisconnectIssueSlackOAuth(ctx, app.ID, owner.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = service.Finish(ctx, pending, app.ID, owner.ID); err == nil {
		t.Fatal("disconnect resurrected token")
	}
	credential, _ = s.IssueSlackInstallationCredential(ctx, app.ID, owner.ID)
	if credential.Status != "disconnected" || len(credential.TokenSealed) != 0 {
		t.Fatal(credential)
	}
	dests, _ = s.ListIssueSlackDestinations(ctx, app.ID, owner.ID)
	if dests[0].Enabled {
		t.Fatal("destination survived disconnect")
	}
	policy, _ = s.GetIssueSlackInteractionPolicy(ctx, app.ID, owner.ID)
	if policy.Enabled {
		t.Fatal("buttons survived disconnect")
	}
	install(app, owner.ID, 2)
	install(app2, other.ID, 0)
	// Delayed events from before reconnection cannot revoke either replacement.
	if err = s.RevokeIssueSlackOAuth(ctx, cfg.SlackAppID, row.WorkspaceID, "", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	row, _ = s.GetIssueSlackInstallation(ctx, app.ID, owner.ID)
	if row.Status != "connected" {
		t.Fatal(row)
	}
	if err = s.RevokeIssueSlackOAuth(ctx, cfg.SlackAppID, row.WorkspaceID, "U9999999999", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	row, _ = s.GetIssueSlackInstallation(ctx, app.ID, owner.ID)
	if row.Status != "connected" {
		t.Fatal("other bot revocation", row)
	}
	if err = s.RevokeIssueSlackOAuth(ctx, cfg.SlackAppID, row.WorkspaceID, "U0123456789", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, a := range []state.App{app, app2} {
		got, e := s.IssueSlackInstallationCredential(ctx, a.ID, a.AccountID)
		if e != nil || got.Status != "revoked" || len(got.TokenSealed) != 0 {
			t.Fatal(got, e)
		}
	}
	row, _ = s.GetIssueSlackInstallation(ctx, app.ID, owner.ID)
	before := row.Revision
	if err = s.RevokeIssueSlackOAuth(ctx, cfg.SlackAppID, row.WorkspaceID, "", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	row, _ = s.GetIssueSlackInstallation(ctx, app.ID, owner.ID)
	if row.Revision != before {
		t.Fatal("revocation replay", row)
	}
	// Only one concurrent confirmation can install an exchanged capability.
	n := start(app, owner.ID, before)
	exchange(n, owner.ID)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- service.Finish(ctx, n, app.ID, owner.ID) }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("concurrent confirmation", successes)
	}
	// Delivery rejection stops future sends and invalidates an exchanged consent.
	row, _ = s.GetIssueSlackInstallation(ctx, app.ID, owner.ID)
	dests, _ = s.ListIssueSlackDestinations(ctx, app.ID, owner.ID)
	destination.Revision = dests[0].Revision
	if err = service.SetDestination(ctx, app.ID, owner.ID, destination, row.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetIssueApplicationSlackPolicy(ctx, app.ID, owner.ID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: team.ID}); err != nil {
		t.Fatal(err)
	}
	dep := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC()
	if _, err = s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: owner.ID, AppID: app.ID, DeploymentID: dep.ID, Environment: "application"}, Event: api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "OAuthError", SourceKind: "exception"}, Fingerprint: strings.Repeat("a", 64), Title: "OAuthError", PayloadHash: strings.Repeat("f", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now}); err != nil {
		t.Fatal(err)
	}
	claim, found, err := s.ClaimIssueSlackDelivery(ctx)
	if err != nil || !found || claim.Status != "in_flight" {
		t.Fatal(claim, found, err)
	}
	if err = s.FinishIssueSlackDelivery(ctx, claim, state.IssueSlackResult{Status: "dead", Error: "invalid_auth", ResponseCode: 200}); err != nil {
		t.Fatal(err)
	}
	row, _ = s.IssueSlackInstallationCredential(ctx, app.ID, owner.ID)
	if row.Status != "revoked" || len(row.TokenSealed) != 0 {
		t.Fatal("rejected token remained connected", row)
	}
	// Expired flows never contact Slack.
	row, _ = s.GetIssueSlackInstallation(ctx, app.ID, owner.ID)
	expired := start(app, owner.ID, row.Revision)
	if _, err = pool.Exec(ctx, "UPDATE issue_slack_oauth_flows SET expires_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	calls := exchanges
	if err = service.Exchange(ctx, expired, owner.ID, "code"); err == nil || calls != exchanges {
		t.Fatal("expired exchange", err)
	}
	if err = s.CleanupIssueSlackThreads(ctx); err != nil {
		t.Fatal(err)
	}
	var flowCount int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM issue_slack_oauth_flows WHERE app_id=$1", app.ID).Scan(&flowCount); err != nil || flowCount != 0 {
		t.Fatal("expired secret retained", flowCount, err)
	}
	// Current owner state gates reconfiguration.
	if _, err = pool.Exec(ctx, "UPDATE accounts SET abuse_hold_at=clock_timestamp(),abuse_hold_reason='operator' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.Start(ctx, app.ID, owner.ID, row.Revision); err == nil {
		t.Fatal("held owner started OAuth")
	}
}
