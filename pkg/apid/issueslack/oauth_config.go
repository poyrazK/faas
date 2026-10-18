package issueslack

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// OAuthConfig contains operator credentials for the Gregale-managed Slack app.
// Token rotation must be disabled in that app; rotating credentials are rejected.
type OAuthConfig struct{ ClientID, ClientSecret, RedirectURI, SlackAppID, SigningSecret string }

func (c OAuthConfig) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURI != "" && c.SlackAppID != "" && ValidSigningSecret(c.SigningSecret)
}
func LoadOAuthConfig(getenv func(string) string) (OAuthConfig, error) {
	c := OAuthConfig{ClientID: getenv("FAAS_ISSUE_SLACK_CLIENT_ID"), ClientSecret: getenv("FAAS_ISSUE_SLACK_CLIENT_SECRET"), RedirectURI: getenv("FAAS_ISSUE_SLACK_REDIRECT_URI"), SlackAppID: getenv("FAAS_ISSUE_SLACK_APP_ID"), SigningSecret: getenv("FAAS_ISSUE_SLACK_SIGNING_SECRET")}
	if c == (OAuthConfig{}) {
		return c, nil
	}
	u, err := url.Parse(c.RedirectURI)
	if !c.Enabled() || !regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(c.ClientID) || !ValidSigningSecret(c.ClientSecret) || !regexp.MustCompile(`^A[A-Z0-9]{8,31}$`).MatchString(c.SlackAppID) || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/oauth/slack/callback" || u.RawPath != "" || len(c.RedirectURI) > api.IssueSlackOAuthRedirectMaxBytes {
		return OAuthConfig{}, fmt.Errorf("set all FAAS_ISSUE_SLACK OAuth variables with a valid HTTPS /oauth/slack/callback URL and Slack app credentials")
	}
	return c, nil
}

const oauthScopes = "chat:write,channels:read,groups:read"

func (c OAuthConfig) Authorize(nonce string) string {
	return "https://slack.com/oauth/v2/authorize?" + url.Values{"client_id": {c.ClientID}, "redirect_uri": {c.RedirectURI}, "scope": {oauthScopes}, "state": {nonce}}.Encode()
}
func hasOAuthScopes(value string) bool {
	scopes := map[string]bool{}
	for _, s := range strings.Split(value, ",") {
		scopes[strings.TrimSpace(s)] = true
	}
	for _, s := range strings.Split(oauthScopes, ",") {
		if !scopes[s] {
			return false
		}
	}
	return true
}
