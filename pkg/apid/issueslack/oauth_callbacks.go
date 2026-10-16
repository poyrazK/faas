package issueslack

import (
	"encoding/json"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type OAuthEvent struct {
	Challenge, WorkspaceID string
	Bots                   []string
	Uninstalled            bool
	Time                   time.Time
}

func ParseOAuthEvent(raw []byte, app string) (OAuthEvent, error) {
	var p struct {
		Type, Challenge string
		App             string `json:"api_app_id"`
		Team            string `json:"team_id"`
		EventTime       int64  `json:"event_time"`
		Event           struct {
			Type   string
			Tokens struct {
				Bot []string `json:"bot"`
			} `json:"tokens"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return OAuthEvent{}, state.ErrInvalidArgument
	}
	if p.Type == "url_verification" && len(p.Challenge) > 0 && len(p.Challenge) <= api.IssueSlackOAuthNameMaxBytes {
		return OAuthEvent{Challenge: p.Challenge}, nil
	}
	if p.Type != "event_callback" || p.App != app || !oauthWorkspacePattern.MatchString(p.Team) || p.EventTime <= 0 || p.EventTime > time.Now().Unix()+1 {
		return OAuthEvent{}, state.ErrInvalidArgument
	}
	out := OAuthEvent{WorkspaceID: p.Team, Time: time.Unix(p.EventTime, 0).Add(time.Second - time.Nanosecond)}
	switch p.Event.Type {
	case "app_uninstalled":
		out.Uninstalled = true
	case "tokens_revoked":
		for _, id := range p.Event.Tokens.Bot {
			if !state.ValidIssueSlackUser(id) {
				return OAuthEvent{}, state.ErrInvalidArgument
			}
			out.Bots = append(out.Bots, id)
		}
	default: // Acknowledge other signed events without retaining their evidence.
	}
	return out, nil
}
func OAuthActionHint(raw []byte, app string) (state.IssueSlackInteractionRequest, error) {
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		return state.IssueSlackInteractionRequest{}, state.ErrInvalidArgument
	}
	var p struct {
		Team struct{ ID string } `json:"team"`
	}
	if json.Unmarshal([]byte(form.Get("payload")), &p) != nil || !oauthWorkspacePattern.MatchString(p.Team.ID) {
		return state.IssueSlackInteractionRequest{}, state.ErrInvalidArgument
	}
	return ParseInteraction(raw, state.IssueSlackInteractionPolicy{WorkspaceID: p.Team.ID, SlackAppID: app})
}
