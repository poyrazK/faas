//go:build !no_pg

package apidserver_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func TestSlackRuleRoutingDashboardPreviewAndActionsPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "slack-rules")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	st := state.NewPgStore(*e.Pool())
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient, oldIdentities := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return id.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{id} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIdentities })
	cfg := issueslack.OAuthConfig{ClientID: "123.456", ClientSecret: strings.Repeat("c", 32), RedirectURI: "https://gregale.example/oauth/slack/callback", SlackAppID: "A0123456789", SigningSecret: strings.Repeat("a", 32)}
	sent := 0
	deliveryID := ""
	privateText := ""
	client := issueslack.NewClient(&http.Client{Transport: slackInstallHTTPTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.URL.Host == "hooks.slack.com" {
			raw, _ := io.ReadAll(r.Body)
			var reply struct{ Text string }
			if json.Unmarshal(raw, &reply) != nil {
				t.Fatal("invalid private reply")
			}
			privateText = reply.Text
			body = "ok"
		} else {
			if r.URL.Host != "slack.com" {
				t.Fatal("unexpected provider")
			}
			switch r.URL.Path {
			case "/api/oauth.v2.access":
				body = `{"ok":true,"access_token":"xoxb-private-token","token_type":"bot","app_id":"A0123456789","bot_user_id":"U0123456789","scope":"chat:write,channels:read,groups:read","team":{"id":"T0123456789","name":"Workspace"}}`
			case "/api/auth.test":
				body = `{"ok":true,"team_id":"T0123456789","user_id":"U0123456789","bot_id":"B0123456789"}`
			case "/api/conversations.list":
				body = `{"ok":true,"channels":[{"id":"C0123456789","name":"default","is_member":true},{"id":"C1111111111","name":"override<script>","is_member":true},{"id":"C2222222222","name":"catchall","is_member":true}]}`
			case "/api/conversations.info":
				_ = r.ParseForm()
				channel := r.PostForm.Get("channel")
				body = fmt.Sprintf(`{"ok":true,"channel":{"id":%q,"is_member":%t}}`, channel, channel != "C9999999999")
			case "/api/chat.postMessage":
				sent++
				raw, _ := io.ReadAll(r.Body)
				var p struct {
					Channel string
					ID      string `json:"client_msg_id"`
				}
				if json.Unmarshal(raw, &p) != nil {
					t.Fatal("invalid Slack message")
				}
				deliveryID = p.ID
				if p.Channel != "C1111111111" || !strings.Contains(string(raw), "Channel routing rule: primary") || !strings.Contains(string(raw), "gregale_acknowledge") || strings.Contains(string(raw), "private-evidence") {
					t.Fatal("bad routed message", string(raw))
				}
				body = `{"ok":true,"channel":"C1111111111","ts":"123.456"}`
			default:
				t.Fatal(r.URL)
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	apidserver.IssueSetSlackOAuth(e, cfg, client)
	*(*e.S()).IssueSlackResponses() = client
	service := issueslack.OAuthService{Store: st, Client: client, Config: cfg, Recipients: []*age.X25519Recipient{id.Recipient()}, Identities: []*age.X25519Identity{id}}
	nonce, _, err := service.Start(t.Context(), app.ID, app.AccountID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Exchange(t.Context(), nonce, app.AccountID, "code"); err != nil {
		t.Fatal(err)
	}
	if err = service.Finish(t.Context(), nonce, app.ID, app.AccountID); err != nil {
		t.Fatal(err)
	}
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{app.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetDestination(t.Context(), app.ID, app.AccountID, state.PutIssueSlackDestination{TeamID: team.ID, ChannelID: "C0123456789", Enabled: true}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: team.ID}); err != nil {
		t.Fatal(err)
	}
	pageURL := "/dashboard/apps/" + app.Slug + "/issues/slack-routing"
	settingsURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	cookieFrom := func(w *httptest.ResponseRecorder) *http.Cookie {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == apidserver.DashboardIssueCookie {
				return c
			}
		}
		t.Fatal("missing CSRF cookie")
		return nil
	}
	page := get(pageURL)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Preview draft routing") || strings.Contains(page.Body.String(), "<script>") || strings.Contains(page.Body.String(), "xoxb-") {
		t.Fatal(page.Code, page.Body.String())
	}
	csrf := cookieFrom(page)
	post := func(form url.Values, valid bool) *httptest.ResponseRecorder {
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		r := httptest.NewRequest("POST", settingsURL, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(csrf)
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	form := func(rev, action string) url.Values {
		return url.Values{"settings_kind": {"slack_routing"}, "routing_action": {action}, "expected_revision": {rev}, "installation_revision": {"1"}, "rule_key": {"primary", "catchall"}, "rule_enabled": {"1", "1"}, "rule_event": {"created", "created"}, "rule_environment": {"application", ""}, "rule_frequency_key": {"", ""}, "rule_team_id": {"", ""}, "rule_channel_id": {"C1111111111", "C2222222222"}, "sample_event": {"created"}, "sample_environment": {"application"}}
	}
	if w := post(form("0", "save"), false); w.Code != 400 {
		t.Fatal("CSRF", w.Code)
	}
	preview := post(form("0", "preview"), true)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Selected rule: <strong>primary") || sent != 0 {
		t.Fatal("preview", preview.Code, preview.Body.String())
	}
	csrf = cookieFrom(preview)
	if p, _ := st.GetIssueSlackRoutingPolicy(t.Context(), app.ID, app.AccountID); p.Revision != 0 || len(p.Rules) != 0 {
		t.Fatal("preview persisted", p)
	}
	move := form("0", "")
	move.Set("rule_move", "up:catchall")
	moved := post(move, true)
	if moved.Code != 200 || strings.Index(moved.Body.String(), "<legend>Rule catchall") > strings.Index(moved.Body.String(), "<legend>Rule primary") {
		t.Fatal("reorder", moved.Code, moved.Body.String())
	}
	csrf = cookieFrom(moved)
	if p, _ := st.GetIssueSlackRoutingPolicy(t.Context(), app.ID, app.AccountID); p.Revision != 0 {
		t.Fatal("reorder persisted", p)
	}
	bad := form("0", "save")
	bad["rule_channel_id"][0] = "C9999999999"
	if w := post(bad, true); w.Code != 400 {
		t.Fatal("unjoined channel", w.Code)
	}
	if w := post(form("0", "save"), true); w.Code != 303 {
		t.Fatal("save", w.Code, w.Body.String())
	}
	if w := post(form("0", "save"), true); w.Code != 409 {
		t.Fatal("stale save", w.Code)
	}
	if w := post(form("1", "save"), true); w.Code != 303 {
		t.Fatal("no-op", w.Code, w.Body.String())
	}
	if p, _ := st.GetIssueSlackRoutingPolicy(t.Context(), app.ID, app.AccountID); p.Revision != 1 {
		t.Fatal("no-op changed revision", p)
	}
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	ingest := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "RoutingHttpError", Message: "private-evidence", SourceKind: "exception"}, map[string]string{"Authorization": "Bearer " + token.Token})
	if ingest.Code != 202 {
		t.Fatal(ingest.Code, ingest.Body.String())
	}
	worker := issueslack.Worker{Store: st, Client: client, OpenToken: func(b []byte) (string, error) { return issueslack.OpenToken([]*age.X25519Identity{id}, b) }, DashboardBase: "https://gregale.example"}
	if ok, e := worker.Step(t.Context()); e != nil || !ok || sent != 1 {
		t.Fatal(ok, sent, e)
	}
	deliveries, err := st.ListIssueSlackDeliveries(t.Context(), app.ID, app.AccountID)
	if err != nil || len(deliveries) != 1 || deliveries[0].RoutingRuleKey != "primary" || deliveries[0].ChannelID != "C1111111111" {
		t.Fatal(deliveries, err)
	}
	issueID := deliveries[0].IssueID
	history := get(settingsURL)
	if history.Code != 200 || !strings.Contains(history.Body.String(), "routing rule <strong>primary") || !strings.Contains(history.Body.String(), "C1111111111") {
		t.Fatal("routing history", history.Code, history.Body.String())
	}
	callback := func(ts string) *httptest.ResponseRecorder {
		payload := fmt.Sprintf(`{"type":"block_actions","api_app_id":"A0123456789","team":{"id":"T0123456789"},"user":{"id":"U0123456789"},"channel":{"id":"C1111111111"},"container":{"type":"message","channel_id":"C1111111111","message_ts":"123.456"},"actions":[{"action_id":"gregale_acknowledge","value":%q,"action_ts":%q}],"response_url":"https://hooks.slack.com/actions/T0123456789/action/token"}`, deliveryID, ts)
		raw := url.Values{"payload": {payload}}.Encode()
		r := httptest.NewRequest("POST", "/integrations/slack/actions", strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(cfg.SigningSecret))
		_, _ = mac.Write([]byte("v0:" + timestamp + ":" + raw))
		r.Header.Set("X-Slack-Request-Timestamp", timestamp)
		r.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	if w := callback("123.789"); w.Code != 200 {
		t.Fatal("routed action link", w.Code, w.Body.String())
	}
	link := regexp.MustCompile(`slack_link=([0-9a-f]{64})`).FindStringSubmatch(privateText)
	if len(link) != 2 {
		t.Fatal("missing private link", privateText)
	}
	if err = st.ConfirmIssueSlackAccountLink(t.Context(), app.ID, app.AccountID, app.AccountID, issueID, link[1]); err != nil {
		t.Fatal(err)
	}
	if w := callback("123.790"); w.Code != 200 {
		t.Fatal("routed action", w.Code, w.Body.String())
	}
	triage, err := st.GetIssueApplicationTriage(t.Context(), app.ID, app.AccountID, app.AccountID, issueID)
	if err != nil || triage.AcknowledgedAt == nil {
		t.Fatal("override action did not acknowledge", triage, err)
	}
	if err = st.DisconnectIssueSlackOAuth(t.Context(), app.ID, app.AccountID, 1); err != nil {
		t.Fatal(err)
	}
	disconnected := get(pageURL)
	if disconnected.Code != 200 || !strings.Contains(disconnected.Body.String(), "Connect Slack before") || strings.Contains(disconnected.Body.String(), `name="rule_key"`) {
		t.Fatal("disconnected editor", disconnected.Code, disconnected.Body.String())
	}
	if w := post(form("1", "save"), true); w.Code != 409 {
		t.Fatal("disconnected save", w.Code)
	}
}
