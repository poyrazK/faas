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

type slackInstallHTTPTransport func(*http.Request) (*http.Response, error)

func (f slackInstallHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSlackOAuthDashboardAndSignedCallbacksPostgres(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "slack-install")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	st := state.NewPgStore(*e.Pool())
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	oldRecipient, oldIdentities := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldRecipient; *apidserver.MfaIdentities = oldIdentities })
	cfg := issueslack.OAuthConfig{ClientID: "123.456", ClientSecret: strings.Repeat("c", 32), RedirectURI: "https://gregale.example/oauth/slack/callback", SlackAppID: "A0123456789", SigningSecret: strings.Repeat("a", 32)}
	exchanges := 0
	deliveryID := ""
	privateReplies := 0
	client := issueslack.NewClient(&http.Client{Transport: slackInstallHTTPTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		if r.URL.Host == "hooks.slack.com" {
			privateReplies++
			body = "ok"
		} else {
			if r.URL.Host != "slack.com" {
				t.Fatal("untrusted provider URL")
			}
			switch r.URL.Path {
			case "/api/oauth.v2.access":
				exchanges++
				body = `{"ok":true,"access_token":"xoxb-private-token","token_type":"bot","app_id":"A0123456789","bot_user_id":"U0123456789","scope":"chat:write,channels:read,groups:read","team":{"id":"T0123456789","name":"Workspace <script>"}}`
			case "/api/auth.test":
				body = `{"ok":true,"team_id":"T0123456789","user_id":"U0123456789","bot_id":"B0123456789"}`
			case "/api/conversations.list":
				body = `{"ok":true,"channels":[{"id":"C0123456789","name":"oncall<script>","is_member":true}]}`
			case "/api/conversations.info":
				_ = r.ParseForm()
				valid := r.PostForm.Get("channel") == "C0123456789"
				body = fmt.Sprintf(`{"ok":true,"channel":{"id":"C0123456789","is_member":%t}}`, valid)
			case "/api/chat.postMessage":
				raw, _ := io.ReadAll(r.Body)
				var p map[string]any
				_ = json.Unmarshal(raw, &p)
				deliveryID = p["client_msg_id"].(string)
				if !strings.Contains(string(raw), "gregale_acknowledge") {
					t.Fatal("no managed actions")
				}
				body = `{"ok":true,"channel":"C0123456789","ts":"123.456"}`
			default:
				t.Fatal(r.URL)
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	apidserver.IssueSetSlackOAuth(e, cfg, client)
	*(*e.S()).IssueSlackResponses() = client
	settingsURL := "/dashboard/apps/" + app.Slug + "/issues/tracker-notifications"
	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		e.AddAdminSession(t, r)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	findCookie := func(w *httptest.ResponseRecorder, name string) *http.Cookie {
		t.Helper()
		for _, c := range w.Result().Cookies() {
			if c.Name == name {
				return c
			}
		}
		t.Fatal("cookie missing", name)
		return nil
	}
	settings := get(settingsURL)
	if settings.Code != 200 || !strings.Contains(settings.Body.String(), "Connect Slack") {
		t.Fatal(settings.Code, settings.Body.String())
	}
	csrf := findCookie(settings, apidserver.DashboardIssueCookie)
	post := func(form url.Values, valid bool, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		if valid {
			form.Set("csrf_token", csrf.Value)
		}
		r := httptest.NewRequest("POST", settingsURL, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(csrf)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		e.AddAdminSession(t, r)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	startForm := func(rev string) url.Values {
		return url.Values{"settings_kind": {"slack_oauth_start"}, "expected_revision": {rev}}
	}
	if w := post(startForm("0"), false); w.Code != 400 {
		t.Fatal("start CSRF", w.Code)
	}
	start := post(startForm("0"), true)
	if start.Code != 303 {
		t.Fatal(start.Code, start.Body.String())
	}
	consent, _ := url.Parse(start.Header().Get("Location"))
	nonce := consent.Query().Get("state")
	oauthCookie := findCookie(start, "faas_issue_slack_oauth")
	if !oauthCookie.Secure || !oauthCookie.HttpOnly || oauthCookie.SameSite != http.SameSiteLaxMode || !issueslack.ValidOAuthNonce(nonce) || consent.Host != "slack.com" {
		t.Fatal("unsafe consent")
	}
	callback := "/oauth/slack/callback?state=" + nonce + "&code=code"
	if w := get(callback); w.Code != 400 || exchanges != 0 {
		t.Fatal("missing state cookie", w.Code)
	}
	if w := get(callback, oauthCookie); w.Code != 303 || exchanges != 1 || strings.Contains(w.Header().Get("Location"), "code=") {
		t.Fatal("exchange", w.Code, w.Body.String())
	}
	if w := get(callback, oauthCookie); w.Code < 400 || exchanges != 1 {
		t.Fatal("exchange replay", w.Code)
	}
	picker := get("/oauth/slack/callback?state="+nonce, oauthCookie)
	if picker.Code != 200 || strings.Contains(picker.Body.String(), "<script>") || strings.Contains(picker.Body.String(), "xoxb-") || picker.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal(picker.Code, picker.Body.String())
	}
	csrf = findCookie(picker, apidserver.DashboardIssueCookie)
	if got, _ := st.GetIssueSlackInstallation(t.Context(), app.ID, app.AccountID); got.Status != "" {
		t.Fatal("GET installed")
	}
	finish := url.Values{"settings_kind": {"slack_oauth_finish"}, "expected_revision": {"0"}, "state": {nonce}}
	if w := post(finish, false, oauthCookie); w.Code != 400 {
		t.Fatal("finish CSRF", w.Code)
	}
	if w := post(finish, true, oauthCookie); w.Code != 303 {
		t.Fatal("finish", w.Code, w.Body.String())
	}
	if w := post(finish, true, oauthCookie); w.Code < 400 {
		t.Fatal("confirmation replay")
	}
	connected := get(settingsURL)
	if connected.Code != 200 || !strings.Contains(connected.Body.String(), "oncall&lt;script&gt;") || strings.Contains(connected.Body.String(), "name=\"bot_token\"") || strings.Contains(connected.Body.String(), cfg.SigningSecret) {
		t.Fatal(connected.Code, connected.Body.String())
	}
	csrf = findCookie(connected, apidserver.DashboardIssueCookie)
	team, err := st.PutIssueTeam(t.Context(), app.ID, app.AccountID, app.AccountID, "responders", api.PutIssueTeamRequest{Name: "Responders", MemberAccountIDs: []string{app.AccountID}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	destination := url.Values{"settings_kind": {"slack_oauth_destination"}, "expected_revision": {"1"}, "destination_revision": {"0"}, "team_id": {team.ID}, "channel_id": {"C9999999999"}, "enabled": {"1"}}
	if w := post(destination, true); w.Code != 400 {
		t.Fatal("unjoined channel", w.Code)
	}
	destination.Set("channel_id", "C0123456789")
	if w := post(destination, true); w.Code != 303 {
		t.Fatal("channel selection", w.Code, w.Body.String())
	}
	_, err = st.SetIssueApplicationSlackPolicy(t.Context(), app.ID, app.AccountID, state.PutIssueApplicationSlackPolicy{Enabled: true, Events: []string{"created"}, FallbackTeamID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	ingest := e.Do(t, "POST", "/v1/apps/"+app.Slug+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "InstallError", SourceKind: "exception"}, map[string]string{"Authorization": "Bearer " + token.Token})
	if ingest.Code != 202 {
		t.Fatal(ingest.Code)
	}
	worker := issueslack.Worker{Store: st, Client: client, OpenToken: func(b []byte) (string, error) { return issueslack.OpenToken([]*age.X25519Identity{identity}, b) }, DashboardBase: "https://gregale.example"}
	if ok, e := worker.Step(t.Context()); e != nil || !ok {
		t.Fatal(ok, e)
	}
	signed := func(path, body, typ string, valid bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", typ)
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(cfg.SigningSecret))
		_, _ = mac.Write([]byte("v0:" + ts + ":" + body))
		signature := "v0=" + hex.EncodeToString(mac.Sum(nil))
		if !valid {
			signature = "v0=" + strings.Repeat("0", 64)
		}
		r.Header.Set("X-Slack-Request-Timestamp", ts)
		r.Header.Set("X-Slack-Signature", signature)
		w := httptest.NewRecorder()
		(*e.H()).ServeHTTP(w, r)
		return w
	}
	action := fmt.Sprintf(`{"type":"block_actions","api_app_id":"A0123456789","team":{"id":"T0123456789"},"user":{"id":"U0123456789"},"channel":{"id":"C0123456789"},"container":{"type":"message","channel_id":"C0123456789","message_ts":"123.456"},"actions":[{"action_id":"gregale_acknowledge","value":%q,"action_ts":"123.789"}],"response_url":"https://hooks.slack.com/actions/T0123456789/action/token"}`, deliveryID)
	payload := url.Values{"payload": {action}}.Encode()
	if w := signed("/integrations/slack/actions", payload, "application/x-www-form-urlencoded", false); w.Code != 401 || privateReplies != 0 {
		t.Fatal("unsigned action", w.Code)
	}
	if w := signed("/integrations/slack/actions", payload, "application/x-www-form-urlencoded", true); w.Code != 200 || privateReplies != 1 {
		t.Fatal("managed action", w.Code, w.Body.String())
	}
	if w := signed("/integrations/slack/events", `{"type":"url_verification","challenge":"verify"}`, "application/json", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"challenge":"verify"`) {
		t.Fatal("challenge", w.Code)
	}
	event := fmt.Sprintf(`{"type":"event_callback","api_app_id":"A0123456789","team_id":"T0123456789","event_time":%d,"event":{"type":"app_uninstalled"}}`, time.Now().Unix())
	if w := signed("/integrations/slack/events", event, "application/json", false); w.Code != 401 {
		t.Fatal("unsigned uninstall", w.Code)
	}
	if w := signed("/integrations/slack/events", event, "application/json", true); w.Code != 200 {
		t.Fatal("uninstall", w.Code, w.Body.String())
	}
	got, err := st.IssueSlackInstallationCredential(t.Context(), app.ID, app.AccountID)
	if err != nil || got.Status != "revoked" || len(got.TokenSealed) != 0 {
		t.Fatal("uninstall retained credential", got, err)
	}
	if w := signed("/integrations/slack/actions", payload, "application/x-www-form-urlencoded", true); w.Code != 401 || privateReplies != 1 {
		t.Fatal("revoked button survived", w.Code)
	}
	if w := post(url.Values{"settings_kind": {"slack_oauth_disconnect"}, "expected_revision": {"1"}}, true); w.Code != 409 {
		t.Fatal("stale disconnect", w.Code)
	}
	if w := post(url.Values{"settings_kind": {"slack_oauth_disconnect"}, "expected_revision": {"2"}}, true); w.Code != 303 {
		t.Fatal("disconnect", w.Code, w.Body.String())
	}
}
