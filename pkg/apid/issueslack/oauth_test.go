package issueslack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type oauthTransport func(*http.Request) (*http.Response, error)

func (f oauthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func oauthTestConfig() OAuthConfig {
	return OAuthConfig{ClientID: "123.456", ClientSecret: strings.Repeat("a", 32), RedirectURI: "https://gregale.example/oauth/slack/callback", SlackAppID: "A0123456789", SigningSecret: strings.Repeat("b", 32)}
}
func oauthReply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}
func TestSlackOAuthConfiguration(t *testing.T) {
	good := oauthTestConfig()
	values := map[string]string{"FAAS_ISSUE_SLACK_CLIENT_ID": good.ClientID, "FAAS_ISSUE_SLACK_CLIENT_SECRET": good.ClientSecret, "FAAS_ISSUE_SLACK_REDIRECT_URI": good.RedirectURI, "FAAS_ISSUE_SLACK_APP_ID": good.SlackAppID, "FAAS_ISSUE_SLACK_SIGNING_SECRET": good.SigningSecret}
	for _, test := range []struct {
		name, key, value string
		valid            bool
	}{{"ready", "", "", true}, {"partial", "FAAS_ISSUE_SLACK_CLIENT_SECRET", "", false}, {"http", "FAAS_ISSUE_SLACK_REDIRECT_URI", "http://gregale.example/oauth/slack/callback", false}, {"query", "FAAS_ISSUE_SLACK_REDIRECT_URI", "https://gregale.example/oauth/slack/callback?next=evil", false}, {"app", "FAAS_ISSUE_SLACK_APP_ID", "fake", false}, {"secret", "FAAS_ISSUE_SLACK_SIGNING_SECRET", strings.Repeat("z", 32), false}} {
		t.Run(test.name, func(t *testing.T) {
			copyValues := map[string]string{}
			for k, v := range values {
				copyValues[k] = v
			}
			if test.key != "" {
				copyValues[test.key] = test.value
			}
			cfg, err := LoadOAuthConfig(func(k string) string { return copyValues[k] })
			if (err == nil) != test.valid {
				t.Fatal(err)
			}
			if test.valid {
				u, _ := url.Parse(cfg.Authorize("nonce"))
				if u.Host != "slack.com" || u.Query().Get("state") != "nonce" || u.Query().Get("scope") != oauthScopes || strings.Contains(u.String(), good.ClientSecret) {
					t.Fatal(u)
				}
			}
		})
	}
	if cfg, err := LoadOAuthConfig(func(string) string { return "" }); err != nil || cfg.Enabled() {
		t.Fatal(cfg, err)
	}
}
func TestSlackOAuthExchangeChecksIdentityAndPermissions(t *testing.T) {
	good := `{"ok":true,"access_token":"xoxb-safe-token","token_type":"bot","app_id":"A0123456789","bot_user_id":"U0123456789","scope":"chat:write,channels:read,groups:read","team":{"id":"T0123456789","name":"Workspace"}}`
	for _, tc := range []struct {
		name, body, auth string
		valid            bool
	}{{"ready", good, "", true}, {"wrong_app", strings.Replace(good, "A0123456789", "A9999999999", 1), "", false}, {"missing_scope", strings.Replace(good, ",groups:read", "", 1), "", false}, {"rotation", strings.Replace(good, `"ok":true`, `"ok":true,"refresh_token":"refresh"`, 1), "", false}, {"enterprise", strings.Replace(good, `"ok":true`, `"ok":true,"is_enterprise_install":true`, 1), "", false}, {"wrong_identity", good, `{"ok":true,"team_id":"T9999999999","user_id":"U0123456789","bot_id":"B0123456789"}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := NewClient(&http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" || r.URL.Host != "slack.com" || r.Method != "POST" {
					t.Fatal(r.URL)
				}
				switch r.URL.Path {
				case "/api/oauth.v2.access":
					if r.Header.Get("Authorization") != "" {
						t.Fatal("auth leaked")
					}
					_ = r.ParseForm()
					if r.PostForm.Get("client_secret") != oauthTestConfig().ClientSecret || r.PostForm.Get("code") != "code" {
						t.Fatal("wrong exchange")
					}
					return oauthReply(tc.body), nil
				case "/api/auth.test":
					if r.Header.Get("Authorization") != "Bearer xoxb-safe-token" {
						t.Fatal("token missing")
					}
					auth := tc.auth
					if auth == "" {
						auth = `{"ok":true,"team_id":"T0123456789","user_id":"U0123456789","bot_id":"B0123456789"}`
					}
					return oauthReply(auth), nil
				default:
					t.Fatal(r.URL)
					return nil, nil
				}
			})})
			out, err := c.ExchangeOAuth(context.Background(), oauthTestConfig(), "code")
			if (err == nil) != tc.valid {
				t.Fatal(out, err)
			}
			if tc.valid && calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}
func TestSlackOAuthChannelBoundsAndValidation(t *testing.T) {
	calls := 0
	c := NewClient(&http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		_ = r.ParseForm()
		if r.PostForm.Get("limit") != "200" {
			t.Fatal("unbounded page")
		}
		return oauthReply(fmt.Sprintf(`{"ok":true,"channels":[{"id":"C0123456789","name":"oncall<script>","is_member":true},{"id":"C1111111111","name":"hidden","is_member":false},{"id":"C2222222222","name":"old","is_member":true,"is_archived":true}],"response_metadata":{"next_cursor":"page%d"}}`, calls)), nil
	})})
	out, err := c.OAuthChannels(context.Background(), "xoxb-safe-token")
	if err != nil || calls != api.IssueSlackOAuthChannelPages || len(out) != 1 || out[0].Name != "oncall<script>" {
		t.Fatal(out, calls, err)
	}
	for _, body := range []string{`{"ok":true,"channel":{"id":"C0123456789","is_member":false}}`, `{"ok":true,"channel":{"id":"C0123456789","is_member":true,"is_archived":true}}`, `{"ok":true,"channel":{"id":"C9999999999","is_member":true}}`} {
		client := NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) { return oauthReply(body), nil })})
		if client.VerifyOAuthChannel(context.Background(), "xoxb-safe-token", "C0123456789") == nil {
			t.Fatal("unavailable channel accepted")
		}
	}
}
func TestSlackOAuthRejectsRedirectsAndOversizeResponses(t *testing.T) {
	for _, tc := range []struct {
		code int
		body string
	}{{302, ""}, {200, strings.Repeat("x", api.IssueSlackOAuthResponseMaxBytes+1)}, {200, `{"ok":false,"error":"secret-provider-body"}`}} {
		c := NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) {
			r := oauthReply(tc.body)
			r.StatusCode = tc.code
			r.Header = http.Header{"Location": {"https://evil.example"}}
			return r, nil
		})})
		_, err := c.ExchangeOAuth(context.Background(), oauthTestConfig(), "code")
		if !errors.Is(err, ErrOAuthProvider) {
			t.Fatal(err)
		}
	}
}
func TestSlackOAuthEventScope(t *testing.T) {
	raw := fmt.Sprintf(`{"type":"event_callback","api_app_id":"A0123456789","team_id":"T0123456789","event_time":%d,"event":{"type":"tokens_revoked","tokens":{"bot":["U0123456789"],"oauth":["U9999999999"]}}}`, time.Now().Unix())
	e, err := ParseOAuthEvent([]byte(raw), "A0123456789")
	if err != nil || len(e.Bots) != 1 || e.Bots[0] != "U0123456789" {
		t.Fatal(e, err)
	}
	if _, err = ParseOAuthEvent([]byte(raw), "A9999999999"); err == nil {
		t.Fatal("other app accepted")
	}
}

func TestSlackOAuthRejectsOversizeChannelPage(t *testing.T) {
	channels := strings.Repeat(`{"id":"C0123456789","name":"channel","is_member":true},`, api.IssueSlackOAuthChannelPageSize) + `{"id":"C0123456789","name":"channel","is_member":true}`
	client := NewClient(&http.Client{Transport: oauthTransport(func(*http.Request) (*http.Response, error) {
		return oauthReply(`{"ok":true,"channels":[` + channels + `]}`), nil
	})})
	if _, err := client.OAuthChannels(context.Background(), "xoxb-safe-token"); err == nil {
		t.Fatal("oversize channel page accepted")
	}
}
