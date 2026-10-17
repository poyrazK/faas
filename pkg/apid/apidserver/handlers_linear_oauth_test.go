package apidserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type nativeLinearOAuthFake struct {
	workspace                     issuetracker.LinearOAuthWorkspace
	exchanges, refreshes, revokes int
	verifier                      string
	refreshErr, revokeErr         error
}

func (f *nativeLinearOAuthFake) ExchangeLinearOAuth(_ context.Context, _ issuetracker.LinearOAuthConfig, code, verifier string) (issuetracker.LinearOAuthTokens, error) {
	f.exchanges++
	f.verifier = verifier
	if code != "valid_code" {
		return issuetracker.LinearOAuthTokens{}, errors.New("bad code")
	}
	return issuetracker.LinearOAuthTokens{AccessToken: "oauth_access_secret", RefreshToken: "oauth_refresh_secret", ExpiresAt: time.Now().Add(24 * time.Hour)}, nil
}
func (f *nativeLinearOAuthFake) RefreshLinearOAuth(_ context.Context, _ issuetracker.LinearOAuthConfig, refresh string) (issuetracker.LinearOAuthTokens, error) {
	f.refreshes++
	if refresh != "oauth_refresh_secret" && refresh != "rotated_refresh_secret" {
		return issuetracker.LinearOAuthTokens{}, errors.New("wrong refresh token")
	}
	if f.refreshErr != nil {
		return issuetracker.LinearOAuthTokens{}, f.refreshErr
	}
	return issuetracker.LinearOAuthTokens{AccessToken: "rotated_access_secret", RefreshToken: "rotated_refresh_secret", ExpiresAt: time.Now().Add(24 * time.Hour)}, nil
}
func (f *nativeLinearOAuthFake) RevokeLinearOAuth(_ context.Context, refresh string) error {
	f.revokes++
	if refresh == "" {
		return errors.New("missing refresh credential")
	}
	return f.revokeErr
}
func (f *nativeLinearOAuthFake) ListLinearOAuthTeams(context.Context, string) (issuetracker.LinearOAuthWorkspace, error) {
	return f.workspace, nil
}

type oauthStatusProvider struct {
	status        issuetracker.Status
	reads, writes int
}

func (f *oauthStatusProvider) ReadStatus(_ context.Context, url, token string) (issuetracker.Status, error) {
	f.reads++
	if url != f.status.URL || !strings.HasPrefix(token, "Bearer ") {
		return f.status, errors.New("OAuth authentication missing")
	}
	return f.status, nil
}
func (f *oauthStatusProvider) SetStatus(context.Context, issuetracker.Status, string, string, string) (issuetracker.Status, error) {
	f.writes++
	return f.status, errors.New("unexpected provider write")
}

func TestNativeIssueLinearOAuthConsentRefreshFencesAndRevocation(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "linear-oauth")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	reporter := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	base := "/v1/apps/" + app.Slug
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "OAuthError", Message: "failed"}, map[string]string{"Authorization": "Bearer " + reporter.Token}), 202)
	key, _ := age.GenerateX25519Identity()
	oldR, oldI := *apidserver.SetSecretRecipient, *apidserver.MfaIdentities
	*apidserver.SetSecretRecipient = func() *age.X25519Recipient { return key.Recipient() }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{key} }
	t.Cleanup(func() { *apidserver.SetSecretRecipient = oldR; *apidserver.MfaIdentities = oldI })
	cfg := issuetracker.LinearOAuthConfig{ClientID: "linear_client", ClientSecret: "client_secret", RedirectURI: "https://api.gregale.test/oauth/linear/callback"}
	team := uuid.NewString()
	provider := &nativeLinearOAuthFake{workspace: issuetracker.LinearOAuthWorkspace{WorkspaceID: "11111111-1111-4111-8111-111111111111", ViewerID: "33333333-3333-4333-8333-333333333333", Teams: []issuetracker.LinearOAuthTeam{{ID: team, Name: "Payments <script>alert(1)</script>"}}}}
	apidserver.IssueNativeSetLinearOAuth(e, cfg, provider)
	apidserver.IssueNativeSetLinearVerifier(e, &nativeLinearFake{})
	var csrf, nonceCookie *http.Cookie
	request := func(method, path string, form url.Values, nonce *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		var body *strings.Reader
		if form == nil {
			body = strings.NewReader("")
		} else {
			if csrf != nil {
				form.Set("csrf_token", csrf.Value)
			}
			body = strings.NewReader(form.Encode())
		}
		r := httptest.NewRequest(method, path, body)
		e.AddAdminSession(t, r)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if csrf != nil {
			r.AddCookie(csrf)
		}
		if nonce != nil {
			r.AddCookie(nonce)
		}
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, r)
		for _, c := range out.Result().Cookies() {
			if c.Name == apidserver.DashboardIssueCookie {
				csrf = c
			}
			if c.Name == "faas_linear_oauth_state" {
				nonceCookie = c
			}
		}
		return out
	}
	page := "/dashboard/apps/" + app.Slug + "/issues?issue=" + accepted.IssueID
	out := request("GET", page, nil, nil)
	if out.Code != 200 || csrf == nil || !strings.Contains(out.Body.String(), "Connect Linear with OAuth") {
		t.Fatal(out.Code, out.Body.String())
	}
	formPath := "/dashboard/apps/" + app.Slug + "/issues/" + accepted.IssueID + "/linear-connection"
	started := request("POST", formPath, url.Values{"action": {"oauth_start"}, "expected_revision": {"0"}}, nil)
	if started.Code != 303 || nonceCookie == nil || !nonceCookie.Secure || !nonceCookie.HttpOnly {
		t.Fatal("unsafe consent start", started.Code, started.Body.String())
	}
	authorize, err := url.Parse(started.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	nonce := authorize.Query().Get("state")
	cookie := *nonceCookie
	callback := "/oauth/linear/callback?state=" + nonce + "&code=valid_code"
	wrong := cookie
	wrong.Value = strings.Repeat("0", 64)
	if out = request("GET", callback, nil, &wrong); out.Code != 400 || provider.exchanges != 0 {
		t.Fatal("mismatched browser state exchanged code", out.Code)
	}
	store := apidserver.IssueNativeStore(e).(state.LinearOAuthStore)
	if _, err = store.GetLinearOAuthFlow(t.Context(), issuetrackers.LinearOAuthStateHash(nonce), uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("flow crossed account scope", err)
	}
	out = request("GET", callback, nil, &cookie)
	if out.Code != 303 || provider.exchanges != 1 {
		t.Fatal("consent callback failed", out.Code, out.Body.String())
	}
	sum := sha256.Sum256([]byte(provider.verifier))
	if authorize.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("PKCE verifier changed")
	}
	if replay := request("GET", callback, nil, &cookie); replay.Code != 400 || provider.exchanges != 1 {
		t.Fatal("authorization code replayed", replay.Code)
	}
	out = request("GET", "/oauth/linear/callback?state="+nonce, nil, &cookie)
	if out.Code != 200 || strings.Contains(out.Body.String(), "<script>alert") || !strings.Contains(out.Body.String(), "&lt;script&gt;") || strings.Contains(out.Body.String(), "oauth_access_secret") {
		t.Fatal("unsafe team picker", out.Code, out.Body.String())
	}
	if out = request("POST", formPath, url.Values{"action": {"oauth_finish"}, "state": {nonce}, "team_id": {uuid.NewString()}}, &cookie); out.Code != 400 {
		t.Fatal("unlisted team accepted", out.Code)
	}
	if out = request("POST", formPath, url.Values{"action": {"oauth_finish"}, "state": {nonce}, "team_id": {team}}, &cookie); out.Code != 303 {
		t.Fatal("team confirmation failed", out.Code, out.Body.String())
	}
	if out = request("POST", formPath, url.Values{"action": {"oauth_finish"}, "state": {nonce}, "team_id": {team}}, &cookie); out.Code < 400 {
		t.Fatal("team confirmation replayed")
	}
	metadata := apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "GET", base+"/issue-tracker-linear-connection", nil, nil), 200)
	if metadata.AuthMethod != "oauth" || metadata.OAuthStatus != "ready" || metadata.Revision != 1 || metadata.TeamID != team || metadata.AccessTokenExpiresAt == nil {
		t.Fatal(metadata)
	}
	link := apidserver.IssueDecode[api.IssueTrackerLink](t, e.Do(t, "POST", base+"/issues/"+accepted.IssueID+"/tracker-links", api.PutIssueTrackerLinkRequest{URL: "https://linear.app/acme/issue/PAY-42"}, nil), 200)
	statuses := &oauthStatusProvider{status: issuetracker.Status{Provider: "linear", URL: link.URL, ID: uuid.NewString(), TeamID: team, StateID: uuid.NewString(), State: "open"}}
	apidserver.IssueNativeSetClients(e, nil, statuses)
	apidserver.IssueDecode[api.NativeLinearIssueTrackerStatus](t, e.Do(t, "GET", base+"/issues/"+accepted.IssueID+"/tracker-links/"+link.ID+"/native-linear-status", nil, nil), 200)
	account := apidserver.IssueNativeAccount(e)
	service := issuetrackers.LinearOAuthService{Store: store, Provider: provider, Verifier: &nativeLinearFake{}, Config: cfg, Recipient: key.Recipient(), Identities: []*age.X25519Identity{key}}
	getRecord := func() state.IssueTrackerLinearRecord {
		t.Helper()
		r, err := store.GetIssueTrackerLinearConnection(t.Context(), app.ID, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	due := func() state.IssueTrackerLinearRecord {
		t.Helper()
		r := getRecord()
		scope := issuetrackers.LinearCredentialScope{AccountID: account.ID, AppID: app.ID, Identity: issuetracker.LinearIdentity{WorkspaceID: r.Connection.WorkspaceID, ViewerID: r.Connection.ViewerID, TeamID: r.Connection.TeamID}}
		c, err := issuetrackers.OpenLinearOAuthCredential([]*age.X25519Identity{key}, scope, r.SealedCredential)
		if err != nil {
			t.Fatal(err)
		}
		c.Tokens.ExpiresAt = time.Now().Add(20 * time.Second)
		sealed, err := issuetrackers.SealLinearOAuthCredential(key.Recipient(), c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_linear_connections SET sealed_credential=$2,access_token_expires_at=$3 WHERE app_id=$1", app.ID, sealed, c.Tokens.ExpiresAt); err != nil {
			t.Fatal(err)
		}
		return getRecord()
	}
	r := due()
	claim, err := store.ClaimLinearOAuthRefresh(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.Resolve(t.Context(), r); err == nil || provider.refreshes != 0 {
		t.Fatal("concurrent refresh consumed token", err)
	}
	if _, err = store.FinishLinearOAuthRefresh(t.Context(), claim, nil, nil, "refreshing"); err != nil {
		t.Fatal(err)
	}
	provider.refreshErr = errors.New("unknown transport outcome")
	if _, _, err = service.Resolve(t.Context(), getRecord()); err == nil {
		t.Fatal("unknown rotation accepted")
	}
	if h := getRecord(); h.Connection.OAuthStatus != "refreshing" || h.RefreshClaimID != "" {
		t.Fatal("uncertain rotation was not recoverable", h.Connection)
	}
	provider.refreshErr = nil
	token, rotated, err := service.Resolve(t.Context(), getRecord())
	if err != nil || token != "Bearer rotated_access_secret" || rotated.Connection.Revision != 1 || rotated.CredentialGeneration <= r.CredentialGeneration || provider.refreshes != 2 {
		t.Fatal("refresh changed policy revision or lost rotation", rotated.Connection, err)
	}
	if _, err = store.FinishLinearOAuthRefresh(t.Context(), claim, nil, nil, "reconnect_required"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("stale refresh completion overwrote rotation", err)
	}
	r = due()
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_linear_connections SET oauth_status='refreshing',refresh_started_at=now()-interval '31 minutes' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	beforeGrace := provider.refreshes
	if _, _, err = service.Resolve(t.Context(), getRecord()); err == nil || provider.refreshes != beforeGrace || getRecord().Connection.OAuthStatus != "reconnect_required" {
		t.Fatal("uncertain refresh exceeded replay grace")
	}
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_linear_connections SET oauth_status='ready',refresh_started_at=NULL WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	r = due()
	provider.refreshErr = issuetracker.ErrLinearOAuthRejected
	if _, _, err = service.Resolve(t.Context(), r); err == nil || getRecord().Connection.OAuthStatus != "reconnect_required" {
		t.Fatal("revoked grant not blocked", err)
	}
	before := provider.refreshes
	if _, _, err = service.Resolve(t.Context(), getRecord()); err == nil || provider.refreshes != before {
		t.Fatal("revoked grant refreshed again")
	}
	// An uncertain revoke must retain recoverable credentials and block use.
	provider.revokeErr = errors.New("unknown revoke outcome")
	if out = request("POST", formPath, url.Values{"action": {"oauth_revoke"}, "expected_revision": {"1"}}, nil); out.Code != 503 && out.Code != 502 {
		t.Fatal("uncertain revoke accepted", out.Code, out.Body.String())
	}
	if getRecord().Connection.OAuthStatus != "revoking" {
		t.Fatal("unconfirmed revoke did not fence access")
	}
	readsBeforeRevoke := statuses.reads
	blocked := e.Do(t, "GET", base+"/issues/"+accepted.IssueID+"/tracker-links/"+link.ID+"/native-linear-status", nil, nil)
	if blocked.Code < 400 || statuses.reads != readsBeforeRevoke {
		t.Fatal("revoking credential reached provider", blocked.Code)
	}
	provider.revokeErr = nil
	if out = request("POST", formPath, url.Values{"action": {"oauth_revoke"}, "expected_revision": {"1"}}, nil); out.Code != 303 {
		t.Fatal("revoke retry failed", out.Code, out.Body.String())
	}
	if getRecord().Connection.Connected || len(getRecord().SealedCredential) != 0 || statuses.writes != 0 {
		t.Fatal("revocation retained credentials or wrote a ticket")
	}
	// A configuration change during consent must win over the older grant.
	started = request("POST", formPath, url.Values{"action": {"oauth_start"}, "expected_revision": {"2"}}, nil)
	if started.Code != 303 {
		t.Fatal(started.Code, started.Body.String())
	}
	newCookie := *nonceCookie
	newURL, _ := url.Parse(started.Header().Get("Location"))
	newNonce := newURL.Query().Get("state")
	apidserver.IssueDecode[api.IssueTrackerLinearConnection](t, e.Do(t, "PUT", base+"/issue-tracker-linear-connection", api.UpdateIssueTrackerLinearConnectionRequest{ExpectedRevision: 2}, nil), 200)
	if out = request("GET", "/oauth/linear/callback?state="+newNonce+"&code=valid_code", nil, &newCookie); out.Code != 303 {
		t.Fatal(out.Code, out.Body.String())
	}
	if out = request("POST", formPath, url.Values{"action": {"oauth_finish"}, "state": {newNonce}, "team_id": {team}}, &newCookie); out.Code != 409 || getRecord().Connection.Connected {
		t.Fatal("stale consent overwrote configuration", out.Code, out.Body.String())
	}
	started = request("POST", formPath, url.Values{"action": {"oauth_start"}, "expected_revision": {"3"}}, nil)
	if started.Code != 303 {
		t.Fatal(started.Code)
	}
	expiredCookie := *nonceCookie
	expiredURL, _ := url.Parse(started.Header().Get("Location"))
	expiredNonce := expiredURL.Query().Get("state")
	if _, err = (*e.Pool()).Exec(t.Context(), "UPDATE issue_tracker_linear_oauth_flows SET expires_at=now()-interval '1 second' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	exchanges := provider.exchanges
	if out = request("GET", "/oauth/linear/callback?state="+expiredNonce+"&code=valid_code", nil, &expiredCookie); out.Code != 400 || provider.exchanges != exchanges {
		t.Fatal("expired OAuth state exchanged code", out.Code)
	}
	if err = store.CleanupLinearOAuthFlows(t.Context()); err != nil {
		t.Fatal(err)
	}

}
