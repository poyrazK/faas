package issueslack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var oauthWorkspacePattern = regexp.MustCompile(`^T[A-Z0-9]{8,31}$`)
var ErrOAuthProvider = errors.New("slack authorization or channel verification failed")

type OAuthTokens struct {
	Token                                             string `json:"-"`
	WorkspaceID, WorkspaceName, SlackAppID, BotUserID string
}
type OAuthChannel struct{ ID, Name string }

func (c *Client) oauthCall(ctx context.Context, method, token string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrOAuthProvider
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrOAuthProvider
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ErrOAuthProvider
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.IssueSlackOAuthResponseMaxBytes+1))
	if err != nil || len(raw) > api.IssueSlackOAuthResponseMaxBytes {
		return ErrOAuthProvider
	}
	var base struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal(raw, &base) != nil || !base.OK || json.Unmarshal(raw, out) != nil {
		return ErrOAuthProvider
	}
	return nil
}
func (c *Client) ExchangeOAuth(ctx context.Context, cfg OAuthConfig, code string) (OAuthTokens, error) {
	var data struct {
		AccessToken       string                    `json:"access_token"`
		TokenType         string                    `json:"token_type"`
		AppID             string                    `json:"app_id"`
		BotUserID         string                    `json:"bot_user_id"`
		Scope             string                    `json:"scope"`
		RefreshToken      string                    `json:"refresh_token"`
		ExpiresIn         int                       `json:"expires_in"`
		EnterpriseInstall bool                      `json:"is_enterprise_install"`
		Team              struct{ ID, Name string } `json:"team"`
	}
	err := c.oauthCall(ctx, "oauth.v2.access", "", url.Values{"client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "code": {code}, "redirect_uri": {cfg.RedirectURI}}, &data)
	if err != nil || data.TokenType != "bot" || !ValidBotToken(data.AccessToken) || data.AppID != cfg.SlackAppID || !state.ValidIssueSlackUser(data.BotUserID) || !oauthWorkspacePattern.MatchString(data.Team.ID) || len(data.Team.Name) == 0 || len(data.Team.Name) > api.IssueSlackOAuthNameMaxBytes || !hasOAuthScopes(data.Scope) || data.RefreshToken != "" || data.ExpiresIn != 0 || data.EnterpriseInstall {
		return OAuthTokens{}, ErrOAuthProvider
	}
	out := OAuthTokens{Token: data.AccessToken, WorkspaceID: data.Team.ID, WorkspaceName: data.Team.Name, SlackAppID: data.AppID, BotUserID: data.BotUserID}
	if err = c.VerifyOAuth(ctx, out); err != nil {
		return OAuthTokens{}, err
	}
	return out, nil
}
func (c *Client) VerifyOAuth(ctx context.Context, t OAuthTokens) error {
	var auth struct {
		TeamID string `json:"team_id"`
		UserID string `json:"user_id"`
		BotID  string `json:"bot_id"`
	}
	if !ValidBotToken(t.Token) {
		return ErrOAuthProvider
	}
	if err := c.oauthCall(ctx, "auth.test", t.Token, nil, &auth); err != nil || auth.TeamID != t.WorkspaceID || auth.UserID != t.BotUserID || auth.BotID == "" {
		return ErrOAuthProvider
	}
	return nil
}
func (c *Client) OAuthChannels(ctx context.Context, token string) ([]OAuthChannel, error) {
	out := []OAuthChannel{}
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for range api.IssueSlackOAuthChannelPages {
		var data struct {
			Channels []struct {
				ID, Name string
				Member   bool `json:"is_member"`
				Archived bool `json:"is_archived"`
			}
			Meta struct {
				Next string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		err := c.oauthCall(ctx, "conversations.list", token, url.Values{"types": {"public_channel,private_channel"}, "exclude_archived": {"true"}, "limit": {strconv.Itoa(api.IssueSlackOAuthChannelPageSize)}, "cursor": {cursor}}, &data)
		if err != nil {
			return nil, err
		}
		if len(data.Channels) > api.IssueSlackOAuthChannelPageSize {
			return nil, ErrOAuthProvider
		}
		for _, ch := range data.Channels {
			if ch.Member && !ch.Archived && state.ValidIssueSlackChannel(ch.ID) && !seen[ch.ID] && len(ch.Name) > 0 && len(ch.Name) <= api.IssueSlackOAuthNameMaxBytes {
				out = append(out, OAuthChannel{ID: ch.ID, Name: ch.Name})
				seen[ch.ID] = true
			}
		}
		cursor = data.Meta.Next
		if cursor == "" {
			return out, nil
		}
		if len(cursor) > api.IssueSlackOAuthCodeMaxBytes || cursors[cursor] {
			return nil, ErrOAuthProvider
		}
		cursors[cursor] = true
	}
	return out, nil // Bounded first pages; owner may enter another joined channel ID.
}
func (c *Client) VerifyOAuthChannel(ctx context.Context, token, channel string) error {
	if !state.ValidIssueSlackChannel(channel) {
		return ErrOAuthProvider
	}
	var data struct {
		Channel struct {
			ID       string
			Member   bool `json:"is_member"`
			Archived bool `json:"is_archived"`
		}
	}
	if err := c.oauthCall(ctx, "conversations.info", token, url.Values{"channel": {channel}}, &data); err != nil || data.Channel.ID != channel || !data.Channel.Member || data.Channel.Archived {
		return ErrOAuthProvider
	}
	return nil
}
