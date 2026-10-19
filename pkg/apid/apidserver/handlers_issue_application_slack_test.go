//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

type applicationSlackTransport func(*http.Request) (*http.Response, error)

func (f applicationSlackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestApplicationIssueSlackRoutingThreadsAndBoundsPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "application-slack")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	full := apidserver.IssueNativeStore(e)
	st := state.NewPgStore(*e.Pool())
	execSQL := func(query string, args ...any) {
		t.Helper()
		if _, err := (*e.Pool()).Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	org, err := full.CreateOrg(t.Context(), state.Org{Slug: "app-slack-" + uuid.NewString()[:8], Name: "Application responders", Plan: api.PlanPro, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	member, err := full.CreateAccount(t.Context(), "slack-member-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		id   string
		role state.OrgRole
	}{{app.AccountID, state.OrgRoleOwner}, {member.ID, state.OrgRoleDeveloper}} {
		if err := full.AddOrgMember(t.Context(), org.ID, entry.id, entry.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	execSQL("UPDATE apps SET org_id=$2 WHERE id=$1", app.ID, org.ID)
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{member.ID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "fallback", api.PutIssueTeamRequest{Name: "Fallback", MemberAccountIDs: []string{app.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetIssueOwnershipRules(t.Context(), app.ID, app.AccountID, []api.IssueOwnershipRule{{ExceptionType: "AssignedSlackError", AssigneeTeamID: team.ID}}); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient := *apidserver.SetSecretRecipient
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient })
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	getPage := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", pageURL, nil)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	csrf := func() *http.Cookie {
		t.Helper()
		page := getPage()
		if page.Code != 200 {
			t.Fatalf("settings GET=%d %s", page.Code, page.Body.String())
		}
		for _, c := range page.Result().Cookies() {
			if c.Name == apidserver.DashboardIssueCookie {
				return c
			}
		}
		t.Fatal("no CSRF cookie")
		return nil
	}()
	post := func(form url.Values, valid bool) *httptest.ResponseRecorder {
		t.Helper()
		if valid {
			form.Set("csrf_token", csrf.Value)
		} else {
			form.Del("csrf_token")
		}
		r := httptest.NewRequest("POST", pageURL, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(csrf)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	destination := func(teamID, channel string) state.IssueSlackDestination {
		form := url.Values{"settings_kind": {"slack_destination"}, "expected_revision": {"0"}, "team_id": {teamID}, "channel_id": {channel}, "bot_token": {"xoxb-application-fake"}, "enabled": {"1"}}
		if w := post(form, true); w.Code != 303 {
			t.Fatalf("destination save=%d %s", w.Code, w.Body.String())
		}
		all, err := st.ListIssueSlackDestinations(t.Context(), app.ID, app.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range all {
			if d.TeamID == teamID {
				return d
			}
		}
		t.Fatal("missing destination")
		return state.IssueSlackDestination{}
	}
	primary := destination(team.ID, "C0123456789")
	destination(fallback.ID, "C9876543210")
	newEvent := func(kind, key string) api.IssueEvent {
		return api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: kind, Message: "private-title-marker", FingerprintOverride: key}
	}
	send := func(event api.IssueEvent) api.IssueEventResponse {
		t.Helper()
		return apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, app.Slug, token, event), 202)
	}
	deliveries := func() []state.IssueSlackDelivery {
		t.Helper()
		out, err := st.ListIssueSlackDeliveries(t.Context(), app.ID, app.AccountID)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	send(newEvent("AssignedSlackError", "before-opt-in"))
	if len(deliveries()) != 0 {
		t.Fatal("unconfigured application Slack replayed alerts")
	}
	policy, err := st.GetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || policy.Enabled || policy.Revision != 0 {
		t.Fatalf("default policy=%+v %v", policy, err)
	}
	form := url.Values{"settings_kind": {"application_slack"}, "expected_revision": {"0"}, "enabled": {"1"}, "events": {"created", "regressed", "impact_threshold_reached", "frequency_threshold_reached"}, "fallback_team_id": {fallback.ID}}
	if w := post(form, false); w.Code != 400 {
		t.Fatalf("CSRF bypass=%d", w.Code)
	}
	if w := post(form, true); w.Code != 303 {
		t.Fatalf("application policy=%d %s", w.Code, w.Body.String())
	}
	if w := post(form, true); w.Code != 409 {
		t.Fatalf("stale policy=%d", w.Code)
	}
	policy, err = st.GetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries()) != 0 {
		t.Fatal("enabling policy replayed old issue activity")
	}
	if _, err := st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, member.ID, state.PutIssueApplicationSlackPolicy{Revision: policy.Revision}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("member policy write=%v", err)
	}
	if _, err := st.GetIssueApplicationSlackPolicy(t.Context(), app.ID, member.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("member policy read=%v", err)
	}
	for _, events := range [][]string{{}, {"resolved"}, {"created", "created"}} {
		if _, err := st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID, state.PutIssueApplicationSlackPolicy{Revision: policy.Revision, Enabled: true, Events: events}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatal("invalid events accepted", events, err)
		}
	}
	setPolicy := func(enabled bool, events []string, fallbackID string) {
		t.Helper()
		var err error
		policy, err = st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID, state.PutIssueApplicationSlackPolicy{Revision: policy.Revision, Enabled: enabled, Events: events, FallbackTeamID: fallbackID})
		if err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var requests []map[string]any
	mode := "rate"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xoxb-application-fake" {
			t.Error("wrong bot token")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		raw, _ := json.Marshal(payload)
		if strings.Contains(string(raw), "private-title-marker") || strings.Contains(string(raw), "xoxb-") {
			t.Error("private event/token content in Slack message")
		}
		mu.Lock()
		requests = append(requests, payload)
		n := len(requests)
		current := mode
		mu.Unlock()
		if current == "rate" {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return
		}
		if current == "invalid" {
			fmt.Fprint(w, `{"ok":false,"error":"invalid_auth"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": payload["channel"], "ts": fmt.Sprintf("123.%06d", n)})
	}))
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	transport := applicationSlackTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://slack.com/api/chat.postMessage" {
			return nil, fmt.Errorf("unexpected Slack endpoint")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = origin.Scheme
		u.Host = origin.Host
		copy.URL = &u
		return server.Client().Transport.RoundTrip(copy)
	})
	worker := issueslack.Worker{Store: st, Client: issueslack.NewClient(&http.Client{Transport: transport}), OpenToken: func(blob []byte) (string, error) { return issueslack.OpenToken([]*age.X25519Identity{identity}, blob) }, DashboardBase: "https://gregale.dev"}
	forceDue := func() {
		execSQL("UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID)
		execSQL("UPDATE issue_team_slack_deliveries SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE destination_id IN (SELECT id FROM issue_team_slack_destinations WHERE app_id=$1)", app.ID)
	}
	step := func() {
		t.Helper()
		forceDue()
		if ok, err := worker.Step(t.Context()); err != nil || !ok {
			t.Fatalf("worker=%v %v", ok, err)
		}
	}
	assigned := send(newEvent("AssignedSlackError", "assigned"))
	unassigned := send(newEvent("UnassignedSlackError", "unassigned"))
	if got := deliveries(); len(got) != 2 {
		t.Fatalf("created deliveries=%+v", got)
	}
	unchanged, err := st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID, state.PutIssueApplicationSlackPolicy{Revision: policy.Revision, Enabled: true, Events: []string{"frequency_threshold_reached", "impact_threshold_reached", "regressed", "created"}, FallbackTeamID: fallback.ID})
	if err != nil || unchanged.Revision != policy.Revision {
		t.Fatalf("no-op policy changed revision: %+v %v", unchanged, err)
	}
	step()
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	// A rate-limited destination does not block an independent team.
	if ok, err := worker.Step(t.Context()); err != nil || !ok {
		t.Fatalf("fallback worker=%v %v", ok, err)
	}
	step()
	mu.Lock()
	if requests[0]["channel"] != "C0123456789" || requests[1]["channel"] != "C9876543210" || requests[2]["client_msg_id"] != requests[0]["client_msg_id"] {
		t.Fatal("routing or retry identity", requests)
	}
	fallbackPayload, _ := json.Marshal(requests[1])
	if !strings.Contains(string(fallbackPayload), unassigned.IssueID) {
		t.Error("fallback message links to the wrong issue")
	}
	root := fmt.Sprintf("123.%06d", 3)
	mu.Unlock()
	// Frequency, impact and regression activities use the same issue thread.
	zero := int64(0)
	if _, err := st.SetIssueFrequencyAlertPolicy(t.Context(), app.ID, app.AccountID, api.UpdateIssueFrequencyAlertPolicyRequest{ExpectedRevision: &zero, Rules: []api.IssueFrequencyAlertRule{{Key: "burst", Enabled: true, MinimumEvents: 2, WindowSeconds: 300, CooldownSeconds: 600}}}); err != nil {
		t.Fatal(err)
	}
	frequencyEvent := newEvent("AssignedSlackError", "assigned")
	send(frequencyEvent)
	if duplicate := send(frequencyEvent); !duplicate.Duplicate {
		t.Fatal("duplicate capture was not detected")
	}
	if _, err := st.SetIssueImpactAlertPolicy(t.Context(), app.ID, app.AccountID, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	apidserver.IssueSeedRequestAttribution(t, e, app, dep, requestID)
	impactEvent := newEvent("AssignedSlackError", "assigned")
	impactEvent.RequestID = requestID
	send(impactEvent)
	if _, err := st.ActOnIssue(t.Context(), app.ID, assigned.IssueID, app.AccountID, api.IssueActionRequest{Action: "resolve", FixedDeploymentID: dep.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	regression := newEvent("AssignedSlackError", "assigned")
	regression.RequestID = requestID
	if got := send(regression); !got.Regressed {
		t.Fatal("fixed release did not regress")
	}
	for range 3 {
		step()
	}
	mu.Lock()
	for _, payload := range requests[3:] {
		if payload["thread_ts"] != root {
			t.Error("application alert lost its issue thread", payload)
		}
	}
	mu.Unlock()
	if got := deliveries(); len(got) != 5 {
		t.Fatalf("alert types queued incorrectly: %+v", got)
	}
	// Filters do not change the underlying issue activity or generic webhooks.
	setPolicy(true, []string{"created"}, fallback.ID)
	filtered := newEvent("AssignedSlackError", "filtered-impact")
	filtered.RequestID = requestID
	send(filtered)
	step()
	last := deliveries()[0]
	if last.Kind != "created" {
		t.Fatal("filtered impact alert queued", last)
	}
	// Claims serialize destinations; current ignore, owner, membership and
	// assignment checks run again immediately before HTTP.
	cancelClaim := func(c state.IssueSlackClaim) {
		t.Helper()
		if ready, err := st.IssueSlackDeliveryReady(t.Context(), c); err != nil || ready {
			t.Fatalf("obsolete route ready=%v %v", ready, err)
		}
		if err := st.FinishIssueSlackDelivery(t.Context(), c, state.IssueSlackResult{Status: "canceled", Error: "destination_unavailable"}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func() state.IssueSlackClaim {
		t.Helper()
		forceDue()
		c, ok, err := st.ClaimIssueSlackDelivery(t.Context())
		if err != nil || !ok || c.Status != "in_flight" || !c.Application {
			t.Fatalf("claim=%+v found=%v err=%v", c, ok, err)
		}
		return c
	}
	ignored := send(newEvent("AssignedSlackError", "ignored"))
	c := claim()
	until := time.Now().Add(time.Hour)
	if _, err := st.ActOnIssue(t.Context(), app.ID, ignored.IssueID, app.AccountID, api.IssueActionRequest{Action: "ignore", IgnoredUntil: &until}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cancelClaim(c)
	reassigned := send(newEvent("AssignedSlackError", "reassigned"))
	forceDue()
	type claimResult struct {
		c   state.IssueSlackClaim
		ok  bool
		err error
	}
	claims := make(chan claimResult, 2)
	for range 2 {
		go func() { c, ok, err := st.ClaimIssueSlackDelivery(t.Context()); claims <- claimResult{c, ok, err} }()
	}
	wins := 0
	for range 2 {
		r := <-claims
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.ok {
			wins++
			c = r.c
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent claim winners=%d", wins)
	}
	if _, err := st.ActOnIssue(t.Context(), app.ID, reassigned.IssueID, app.AccountID, api.IssueActionRequest{Action: "assign", AssigneeTeamID: fallback.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cancelClaim(c)
	send(newEvent("AssignedSlackError", "membership"))
	c = claim()
	if _, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, team.Key, api.PutIssueTeamRequest{Name: team.Name, MemberAccountIDs: []string{app.AccountID}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cancelClaim(c)
	if _, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, team.Key, api.PutIssueTeamRequest{Name: team.Name, MemberAccountIDs: []string{member.ID}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	send(newEvent("AssignedSlackError", "plan"))
	c = claim()
	execSQL("UPDATE accounts SET plan='free' WHERE id=$1", app.AccountID)
	cancelClaim(c)
	execSQL("UPDATE accounts SET plan='pro' WHERE id=$1", app.AccountID)
	alias := send(newEvent("AssignedSlackError", "alias"))
	c = claim()
	if _, err := st.ActOnIssue(t.Context(), app.ID, alias.IssueID, app.AccountID, api.IssueActionRequest{Action: "merge", TargetIssueID: assigned.IssueID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cancelClaim(c)
	// Policy changes fence old receipts and do not replay history on re-enable.
	send(newEvent("AssignedSlackError", "policy-fence"))
	c = claim()
	before := len(deliveries())
	setPolicy(false, []string{"created"}, fallback.ID)
	if err := st.FinishIssueSlackDelivery(t.Context(), c, state.IssueSlackResult{Status: "succeeded", MessageTS: "123.999999"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatalf("stale policy receipt=%v", err)
	}
	setPolicy(true, []string{"created"}, fallback.ID)
	if len(deliveries()) != before {
		t.Fatal("policy re-enable replayed history")
	}
	// Rotating a destination preserves its captured historical channel and starts
	// fresh threads. In-flight completion cannot overwrite the new revision.
	send(newEvent("AssignedSlackError", "destination-fence"))
	c = claim()
	primary, err = st.SetIssueSlackDestination(t.Context(), app.ID, app.AccountID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C1111111111", Enabled: true, Revision: primary.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishIssueSlackDelivery(t.Context(), c, state.IssueSlackResult{Status: "succeeded", MessageTS: "123.999998"}); !errors.Is(err, state.ErrIssueSlackClaimLost) {
		t.Fatalf("stale destination receipt=%v", err)
	}
	send(newEvent("AssignedSlackError", "new-channel"))
	step()
	mu.Lock()
	latest := requests[len(requests)-1]
	if latest["channel"] != "C1111111111" || latest["thread_ts"] != nil {
		t.Error("rotated destination reused thread", latest)
	}
	mu.Unlock()
	// Retained failures support owner retry with a stable message ID.
	send(newEvent("AssignedSlackError", "manual-retry"))
	mu.Lock()
	mode = "invalid"
	mu.Unlock()
	step()
	failed := deliveries()[0]
	if failed.Status != "dead" || !failed.CanRetry {
		t.Fatal("failed delivery missing retry", failed)
	}
	if err := st.RetryIssueSlackDelivery(t.Context(), app.ID, member.ID, failed.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("member retried owner delivery", err)
	}
	if err := st.RetryIssueSlackDelivery(t.Context(), app.ID, app.AccountID, failed.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	step()
	mu.Lock()
	n := len(requests)
	if requests[n-1]["client_msg_id"] != requests[n-2]["client_msg_id"] {
		t.Error("manual retry changed identity")
	}
	mu.Unlock()
	// No route means no broadcast. Fallback is explicit, including when the
	// assigned team's sole organization member has been removed.
	setPolicy(true, []string{"created"}, "")
	policyBefore := policy.UnroutedAlerts
	send(newEvent("UnassignedSlackError", "no-route"))
	current, err := st.GetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID)
	if err != nil || current.UnroutedAlerts != policyBefore+1 {
		t.Fatalf("missing unrouted count: %+v %v", current, err)
	}
	setPolicy(true, []string{"created"}, fallback.ID)
	execSQL("UPDATE org_memberships SET removed_at=clock_timestamp() WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	send(newEvent("AssignedSlackError", "unavailable-team"))
	step()
	mu.Lock()
	if requests[len(requests)-1]["channel"] != "C9876543210" {
		t.Error("unavailable assigned team did not use fallback")
	}
	mu.Unlock()
	execSQL("UPDATE org_memberships SET removed_at=NULL WHERE org_id=$1 AND account_id=$2", org.ID, member.ID)
	// Enqueue failure rolls back source capture and the independent webhook.
	hook, err := full.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: app.AccountID, AppID: app.ID, TargetURL: "https://example.com/app-alert", Enabled: true, EventFilter: []string{"issue.created"}, SecretSealed: []byte("test-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	execSQL(`CREATE FUNCTION fail_application_slack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.activity_id IS NOT NULL THEN RAISE EXCEPTION 'injected queue failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_application_slack BEFORE INSERT ON issue_team_slack_deliveries FOR EACH ROW EXECUTE FUNCTION fail_application_slack()`)
	retryEvent := newEvent("AssignedSlackError", "rollback")
	if w := apidserver.IssueSend(t, e, app.Slug, token, retryEvent); w.Code != 503 {
		t.Fatalf("enqueue failure status=%d", w.Code)
	}
	var exists bool
	if err := (*e.Pool()).QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM issue_events WHERE app_id=$1 AND event_id=$2)", app.ID, retryEvent.EventID).Scan(&exists); err != nil || exists {
		t.Fatal("failed capture persisted", exists, err)
	}
	execSQL("DROP TRIGGER fail_application_slack ON issue_team_slack_deliveries; DROP FUNCTION fail_application_slack()")
	if got := send(retryEvent); got.Duplicate {
		t.Fatal("rolled-back capture was treated as duplicate")
	}
	step()
	relay := full.(state.AppWebhookEventOutboxStore)
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatalf("webhook relay=%d %v", n, err)
	}
	if ledger, _, err := full.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 10, ""); err != nil || len(ledger) != 1 {
		t.Fatalf("independent webhook ledger=%d %v", len(ledger), err)
	}
	page := getPage()
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Application-error Slack alerts") || strings.Contains(page.Body.String(), "xoxb-application-fake") {
		t.Fatalf("unsafe settings page=%d", page.Code)
	}
	// A second app demonstrates isolated fallback and bounded outage admission.
	other := apidserver.SeedPGApp(t, e, "application-slack-cap")
	otherDep := apidserver.IssueSeedDeployment(t, e, other, 2)
	otherToken := apidserver.IssueCreateToken(t, e, other.Slug, otherDep)
	otherTeam, err := st.PutIssueTeam(t.Context(), other.ID, other.AccountID, other.AccountID, "other", api.PutIssueTeamRequest{Name: "Other", MemberAccountIDs: []string{other.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID, state.PutIssueApplicationSlackPolicy{Revision: policy.Revision, Enabled: true, Events: []string{"created"}, FallbackTeamID: otherTeam.ID}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("cross-app fallback accepted", err)
	}
	otherDestination, err := st.SetIssueSlackDestination(t.Context(), other.ID, other.AccountID, state.PutIssueSlackDestination{TeamID: otherTeam.ID, ChannelID: "C2222222222", Enabled: true, TokenSealed: []byte("unused-fixture-token")})
	if err != nil {
		t.Fatal(err)
	}
	otherPolicy, err := st.SetIssueApplicationSlackPolicy(t.Context(), other.ID, other.AccountID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: otherTeam.ID})
	if err != nil {
		t.Fatal(err)
	}
	otherHook, err := full.CreateAppWebhook(t.Context(), state.AppWebhook{AccountID: other.AccountID, AppID: other.ID, TargetURL: "https://example.com/cap-alert", Enabled: true, EventFilter: []string{"issue.created"}, SecretSealed: []byte("test-sealed")})
	if err != nil {
		t.Fatal(err)
	}
	otherSend := func(key string) api.IssueEventResponse {
		t.Helper()
		return apidserver.IssueDecode[api.IssueEventResponse](t, apidserver.IssueSend(t, e, other.Slug, otherToken, newEvent("CapacityError", key)), 202)
	}
	capRoot := otherSend("capacity-root")
	execSQL(`WITH acts AS (INSERT INTO issue_activity(issue_id,action,details,created_at) SELECT $1,'created','{"capacity_fixture":"1"}',clock_timestamp() FROM generate_series(1,$7::integer) RETURNING id)
 INSERT INTO issue_team_slack_deliveries(id,destination_id,destination_revision,activity_id,incident_id,channel_id,application_policy_revision,recipient_account_ids)
 SELECT gen_random_uuid(),$2,$3,acts.id,$1,$4,$5,ARRAY[$6::uuid] FROM acts`, capRoot.IssueID, otherDestination.ID, otherDestination.Revision, otherDestination.ChannelID, otherPolicy.Revision, other.AccountID, api.IssueApplicationSlackMaxDeliveries-1)
	countOther := func() int {
		t.Helper()
		var count int
		if err := (*e.Pool()).QueryRow(t.Context(), "SELECT count(*) FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id WHERE dst.app_id=$1 AND d.activity_id IS NOT NULL", other.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	otherSend("capacity-full")
	otherPolicy, err = st.GetIssueApplicationSlackPolicy(t.Context(), other.ID, other.AccountID)
	if err != nil || otherPolicy.SkippedDeliveries != 1 || countOther() != api.IssueApplicationSlackMaxDeliveries {
		t.Fatalf("capacity admission: %+v %v count=%d", otherPolicy, err, countOther())
	}
	execSQL("UPDATE issue_team_slack_deliveries SET status='dead',last_error='attempts_exhausted' WHERE id=(SELECT d.id FROM issue_team_slack_deliveries d JOIN issue_team_slack_destinations dst ON dst.id=d.destination_id WHERE dst.app_id=$1 ORDER BY d.delivery_seq LIMIT 1)", other.ID)
	otherSend("capacity-recovered")
	otherPolicy, err = st.GetIssueApplicationSlackPolicy(t.Context(), other.ID, other.AccountID)
	if err != nil || otherPolicy.SkippedDeliveries != 1 || countOther() != api.IssueApplicationSlackMaxDeliveries {
		t.Fatalf("terminal history did not make room: %+v %v", otherPolicy, err)
	}
	if n, err := relay.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 3 {
		t.Fatalf("capacity suppressed webhooks: %d %v", n, err)
	}
	if ledger, _, err := full.ListAppWebhookDeliveries(t.Context(), other.ID, otherHook.ID, 10, ""); err != nil || len(ledger) != 3 {
		t.Fatalf("capacity webhook deliveries=%d %v", len(ledger), err)
	}
	execSQL("UPDATE issue_team_slack_deliveries SET status='canceled',last_error='destination_unavailable',updated_at=clock_timestamp()-interval '8 days' WHERE destination_id=$1", otherDestination.ID)
	if err := st.CleanupIssueSlackThreads(t.Context()); err != nil {
		t.Fatal(err)
	}
	if countOther() != 0 {
		t.Fatal("application terminal retention did not expire")
	}
}
