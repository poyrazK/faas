//go:build !no_pg

package apidserver_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/state"
)

type interactionHTTPTransport func(*http.Request) (*http.Response, error)

func (f interactionHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeSlackInteractionDashboardAndCallbackPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "slack-actions")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	st := state.NewPgStore(*e.Pool())
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient := *apidserver.SetSecretRecipient
	oldIdentities := *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIdentities })
	responseCalls := 0
	lastReply := ""
	failNextReply := false
	*(*e.S()).IssueSlackResponses() = issueslack.NewClient(&http.Client{Transport: interactionHTTPTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "hooks.slack.com" || !strings.HasPrefix(r.URL.Path, "/actions/T0123456789/") || r.Header.Get("Authorization") != "" {
			t.Fatal("unsafe interaction response")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "ephemeral") || !strings.Contains(string(body), "replace_original") {
			t.Fatal(string(body))
		}
		var reply struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(body, &reply) != nil {
			t.Fatal("invalid ephemeral reply")
		}
		lastReply = reply.Text
		responseCalls++
		if failNextReply {
			failNextReply = false
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})})
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	csrfFrom := func(w *httptest.ResponseRecorder) *http.Cookie {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == apidserver.DashboardIssueCookie {
				return c
			}
		}
		t.Fatal("missing CSRF")
		return nil
	}
	settingsURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	settings := get(settingsURL)
	if settings.Code != 200 || !strings.Contains(settings.Body.String(), "Slack response buttons") {
		t.Fatal(settings.Code, settings.Body.String())
	}
	cookie := csrfFrom(settings)
	post := func(path string, form url.Values, cookie *http.Cookie, valid bool) *httptest.ResponseRecorder {
		if valid {
			form.Set("csrf_token", cookie.Value)
		}
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	secret := strings.Repeat("a", 32)
	policyForm := func(rev, signing string) url.Values {
		return url.Values{"settings_kind": {"slack_interactions"}, "expected_revision": {rev}, "enabled": {"1"}, "workspace_id": {"T0123456789"}, "slack_app_id": {"A0123456789"}, "signing_secret": {signing}}
	}
	if w := post(settingsURL, policyForm("0", secret), cookie, false); w.Code != 400 {
		t.Fatal("CSRF", w.Code)
	}
	if w := post(settingsURL, policyForm("0", secret), cookie, true); w.Code != 303 {
		t.Fatal("configure", w.Code, w.Body.String())
	}
	if w := post(settingsURL, policyForm("0", ""), cookie, true); w.Code != 409 {
		t.Fatal("stale settings", w.Code)
	}
	if w := post(settingsURL, policyForm("1", "invalid"), cookie, true); w.Code != 400 {
		t.Fatal("invalid secret", w.Code)
	}
	if w := get(settingsURL); w.Code != 200 || strings.Contains(w.Body.String(), secret) {
		t.Fatal("secret exposed", w.Code)
	}
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{app.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SetIssueSlackDestination(t.Context(), app.ID, app.AccountID, state.PutIssueSlackDestination{TeamID: team.ID, Enabled: true, ChannelID: "C0123456789", TokenSealed: []byte("fake-token")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	ingest := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "ActionError", Message: "private-action-evidence", SourceKind: "exception"}, map[string]string{"Authorization": "Bearer " + token.Token})
	if ingest.Code != 202 {
		t.Fatal(ingest.Code, ingest.Body.String())
	}
	var deliveryID string
	worker := issueslack.Worker{Store: st, OpenToken: func([]byte) (string, error) { return "xoxb-test", nil }, DashboardBase: "https://gregale.dev", Client: issueslack.NewClient(&http.Client{Transport: interactionHTTPTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://slack.com/api/chat.postMessage" {
			t.Fatal("unexpected network destination")
		}
		raw, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		deliveryID = p["client_msg_id"].(string)
		if !strings.Contains(string(raw), "gregale_snooze_1h") || strings.Contains(string(raw), "private-action-evidence") {
			t.Fatal(string(raw))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"channel":"C0123456789","ts":"123.456"}`))}, nil
	})})}
	if ok, err := worker.Step(t.Context()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	issues, err := st.ListIssues(t.Context(), app.ID, state.IssueListFilter{}, state.IssueCursor{})
	if err != nil || len(issues.Items) != 1 {
		t.Fatal(issues, err)
	}
	issue := issues.Items[0].ID
	callbackURL := "/integrations/slack/issues/" + app.ID
	actionID := "gregale_acknowledge"
	payload := func(actionTS, workspace string) string {
		raw, _ := json.Marshal(map[string]any{"type": "block_actions", "response_url": "https://hooks.slack.com/actions/T0123456789/123456789/test-response-token", "api_app_id": "A0123456789", "team": map[string]string{"id": workspace}, "user": map[string]string{"id": "U0123456789"}, "channel": map[string]string{"id": "C0123456789"}, "container": map[string]string{"type": "message", "channel_id": "C0123456789", "message_ts": "123.456"}, "actions": []map[string]string{{"action_id": actionID, "value": deliveryID, "action_ts": actionTS}}})
		return url.Values{"payload": {string(raw)}}.Encode()
	}
	callback := func(raw, ts string, tamper bool) *httptest.ResponseRecorder {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("v0:" + ts + ":" + raw))
		sig := "v0=" + hex.EncodeToString(mac.Sum(nil))
		if tamper {
			raw += "x"
		}
		r := httptest.NewRequest("POST", callbackURL, strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Slack-Request-Timestamp", ts)
		r.Header.Set("X-Slack-Signature", sig)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	raw := payload("124.001", "T0123456789")
	if w := callback(raw, ts, true); w.Code != 401 {
		t.Fatal("tampered body", w.Code)
	}
	if w := callback(raw, strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10), false); w.Code != 401 {
		t.Fatal("stale signature", w.Code)
	}
	if w := callback(payload("124.001", "T9876543210"), ts, false); w.Code != 401 {
		t.Fatal("wrong workspace", w.Code)
	}
	if responseCalls != 0 {
		t.Fatal("unauthenticated callback posted to Slack")
	}
	failNextReply = true
	failed := callback(raw, ts, false)
	if failed.Code != 503 {
		t.Fatal("Slack response retry", failed.Code)
	}
	failedReply := lastReply
	first := callback(raw, ts, false)
	if first.Code != 200 || first.Body.Len() != 0 || lastReply != failedReply {
		t.Fatal(first.Code, first.Body.String())
	}
	match := regexp.MustCompile(`slack_link=([a-f0-9]{64})`).FindStringSubmatch(lastReply)
	if len(match) != 2 {
		t.Fatal("missing confirmation", first.Body.String())
	}
	challenge := match[1]
	triageURL := "/dashboard/apps/" + app.Slug + "/issues/" + issue + "/triage"
	linkPage := get(triageURL + "?slack_link=" + challenge)
	if linkPage.Code != 200 || !strings.Contains(linkPage.Body.String(), "Confirm my Slack account") || linkPage.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(linkPage.Code, linkPage.Body.String())
	}
	linkCookie := csrfFrom(linkPage)
	before, err := st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, issue)
	if err != nil || before.AcknowledgedAt != nil {
		t.Fatal("GET mutated response", before, err)
	}
	if w := post(triageURL, url.Values{"action": {"link_slack"}, "slack_link": {challenge}}, linkCookie, false); w.Code != 400 {
		t.Fatal("link CSRF", w.Code)
	}
	if w := post(triageURL, url.Values{"action": {"link_slack"}, "slack_link": {challenge}}, linkCookie, true); w.Code != 303 {
		t.Fatal("link confirmation", w.Code, w.Body.String())
	}
	if w := callback(raw, ts, false); w.Code != 200 || !strings.Contains(lastReply, "Click the action") {
		t.Fatal("replayed linking click", w.Code, w.Body.String())
	}
	before, err = st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, issue)
	if err != nil || before.AcknowledgedAt != nil {
		t.Fatal("link performed action", before, err)
	}
	raw = payload("125.001", "T0123456789")
	if w := callback(raw, ts, false); w.Code != 200 || !strings.Contains(lastReply, "updated") {
		t.Fatal("linked action", w.Code, w.Body.String())
	}
	after, err := st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, issue)
	if err != nil || after.AcknowledgedBy != app.AccountID || after.Revision != 1 {
		t.Fatal("shared handling", after, err)
	}
	if w := callback(raw, ts, false); w.Code != 200 {
		t.Fatal("retry", w.Code)
	}
	after, err = st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, issue)
	if err != nil || after.Revision != 1 {
		t.Fatal("duplicate mutation", after, err)
	}
	if w := post(settingsURL, policyForm("1", ""), cookie, true); w.Code != 303 {
		t.Fatal("no-op", w.Code)
	}
	if w := post(triageURL, url.Values{"action": {"unlink_slack"}}, linkCookie, true); w.Code != 303 {
		t.Fatal("unlink", w.Code, w.Body.String())
	}
	if responseCalls < 4 {
		t.Fatal("missing Slack private responses", responseCalls)
	}
	link, err := st.GetIssueSlackAccountLink(t.Context(), app.ID, app.AccountID, app.AccountID, issue, "")
	if err != nil || link.UserID != "" {
		t.Fatal(link, err)
	}
	// Claiming uses the same authenticated consent flow, but leaves response
	// handling untouched and recovers after a committed action's reply fails.
	ingest = e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "AssignError", SourceKind: "exception"}, map[string]string{"Authorization": "Bearer " + token.Token})
	created := apidserver.IssueDecode[api.IssueEventResponse](t, ingest, http.StatusAccepted)
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_team_slack_destinations SET next_send_at=clock_timestamp()-interval '1 second' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := worker.Step(t.Context()); err != nil || !ok {
		t.Fatal("assignment message", ok, err)
	}
	actionID = "gregale_assign_to_me"
	raw = payload("200.001", "T0123456789")
	if w := callback(raw, ts, false); w.Code != http.StatusOK {
		t.Fatal("assignment consent", w.Code)
	}
	match = regexp.MustCompile(`slack_link=([a-f0-9]{64})`).FindStringSubmatch(lastReply)
	if len(match) != 2 {
		t.Fatal("missing assignment link", lastReply)
	}
	triageURL = "/dashboard/apps/" + app.Slug + "/issues/" + created.IssueID + "/triage"
	linkPage = get(triageURL + "?slack_link=" + match[1])
	if linkPage.Code != http.StatusOK || !strings.Contains(linkPage.Body.String(), "assigning issues to yourself") {
		t.Fatal("assignment consent scope", linkPage.Code, linkPage.Body.String())
	}
	linkCookie = csrfFrom(linkPage)
	if w := post(triageURL, url.Values{"action": {"link_slack"}, "slack_link": {match[1]}}, linkCookie, false); w.Code != http.StatusBadRequest {
		t.Fatal("assignment consent CSRF", w.Code)
	}
	if w := post(triageURL, url.Values{"action": {"link_slack"}, "slack_link": {match[1]}}, linkCookie, true); w.Code != http.StatusSeeOther {
		t.Fatal("assignment consent confirmation", w.Code, w.Body.String())
	}
	if w := callback(raw, ts, false); w.Code != http.StatusOK || !strings.Contains(lastReply, "Click the action") {
		t.Fatal("replayed assignment link", w.Code, lastReply)
	}
	raw = payload("200.002", "T0123456789")
	failNextReply = true
	if w := callback(raw, ts, false); w.Code != http.StatusServiceUnavailable {
		t.Fatal("committed assignment reply failure", w.Code)
	}
	if w := callback(raw, ts, false); w.Code != http.StatusOK {
		t.Fatal("assignment callback recovery", w.Code)
	}
	detail, err := st.GetIssueDetail(t.Context(), app.ID, created.IssueID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), state.IssueDetailCursors{})
	if err != nil || detail.Issue.AssigneeAccountID != app.AccountID {
		t.Fatal("linked assignment", detail.Issue, err)
	}
	assignments := 0
	for _, activity := range detail.Activity {
		if activity.Action == "assigned" && activity.Details["assignment_source"] == "slack" {
			assignments++
			if activity.ActorAccountID != app.AccountID {
				t.Fatal("wrong assignment actor", activity)
			}
		}
	}
	if assignments != 1 {
		t.Fatal("assignment callback duplicated activity", assignments)
	}
	response, err := st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, created.IssueID)
	if err != nil || response.Revision != 0 || response.AcknowledgedAt != nil || response.SnoozedUntil != nil {
		t.Fatal("assignment mutated response", response, err)
	}
}
