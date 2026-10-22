package issueslack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func ValidSigningSecret(secret string) bool {
	if len(secret) != api.IssueSlackSigningSecretBytes {
		return false
	}
	_, err := hex.DecodeString(secret)
	return err == nil
}
func OpenSigningSecret(identities []*age.X25519Identity, blob []byte) (string, error) {
	namespace, raw, err := secretbox.OpenBytesMulti(identities, blob)
	if err != nil || namespace != state.IssueSlackSigningSecretNamespace || !ValidSigningSecret(string(raw)) {
		return "", state.ErrInvalidArgument
	}
	return string(raw), nil
}
func VerifyInteraction(secret, timestamp, signature string, body []byte, now time.Time) bool {
	if !ValidSigningSecret(secret) || len(body) > api.IssueSlackInteractionMaxBytes || len(signature) != 67 || !strings.HasPrefix(signature, "v0=") {
		return false
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	window := int64(api.IssueSlackSignatureMaxAge / time.Second)
	// Strict second bounds keep the linking receipt alive beyond every accepted
	// replay, including future clock skew and Unix-second rounding.
	if ts <= now.Unix()-window || ts >= now.Unix()+window {
		return false
	}
	got, err := hex.DecodeString(signature[3:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + timestamp + ":"))
	_, _ = mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
func ParseInteraction(body []byte, policy state.IssueSlackInteractionPolicy) (state.IssueSlackInteractionRequest, error) {
	out := state.IssueSlackInteractionRequest{PolicyRevision: policy.Revision}
	form, err := url.ParseQuery(string(body))
	if err != nil || len(form) != 1 || len(form["payload"]) != 1 {
		return out, state.ErrInvalidArgument
	}
	var p struct {
		Type        string `json:"type"`
		App         string `json:"api_app_id"`
		ResponseURL string `json:"response_url"`
		Team        struct {
			ID string `json:"id"`
		} `json:"team"`
		User struct {
			ID     string `json:"id"`
			TeamID string `json:"team_id"`
		} `json:"user"`
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
		Container struct {
			Type      string `json:"type"`
			ChannelID string `json:"channel_id"`
			MessageTS string `json:"message_ts"`
		} `json:"container"`
		Actions []struct {
			ID    string `json:"action_id"`
			Value string `json:"value"`
			TS    string `json:"action_ts"`
		} `json:"actions"`
	}
	if json.Unmarshal([]byte(form.Get("payload")), &p) != nil || p.Type != "block_actions" || p.App != policy.SlackAppID || p.Team.ID != policy.WorkspaceID || !ValidInteractionResponseURL(p.ResponseURL, policy.WorkspaceID) || p.Container.Type != "message" || p.Container.ChannelID != p.Channel.ID || len(p.Actions) != 1 || !state.ValidIssueSlackUser(p.User.ID) || (p.User.TeamID != "" && p.User.TeamID != p.Team.ID) || !state.ValidIssueSlackChannel(p.Channel.ID) || !state.ValidIssueSlackTimestamp(p.Container.MessageTS) {
		return out, state.ErrInvalidArgument
	}
	a := p.Actions[0]
	id, err := uuid.Parse(a.Value)
	if err != nil || id == uuid.Nil || id.String() != a.Value || !state.ValidIssueSlackTimestamp(a.TS) {
		return out, state.ErrInvalidArgument
	}
	actions := map[string]string{"gregale_acknowledge": "acknowledge", "gregale_snooze_1h": "snooze_1h", "gregale_snooze_24h": "snooze_24h"}
	action, ok := actions[a.ID]
	if !ok {
		return out, state.ErrInvalidArgument
	}
	out.ResponseURL = p.ResponseURL
	out.WorkspaceID = policy.WorkspaceID
	out.DeliveryID = a.Value
	out.ChannelID = p.Channel.ID
	out.MessageTS = p.Container.MessageTS
	out.UserID = p.User.ID
	out.Action = action
	sum := sha256.Sum256([]byte(p.Team.ID + "\n" + p.User.ID + "\n" + a.Value + "\n" + a.TS + "\n" + a.ID))
	out.RequestKey = hex.EncodeToString(sum[:])
	return out, nil
}
func InteractionText(result state.IssueSlackInteractionResult, base string) (string, error) {
	switch result.Status {
	case "handled":
		return "Gregale issue response updated. Open Gregale for current status.", nil
	case "already_handled":
		return "This message has already been handled. Open Gregale for current status.", nil
	case "linked":
		return "Slack account linked. Click the action in Slack again to handle the issue.", nil
	case "link_required":
		u, err := url.Parse(base)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", state.ErrInvalidArgument
		}
		link := strings.TrimRight(u.String(), "/") + "/dashboard/apps/" + url.PathEscape(result.AppSlug) + "/issues/" + url.PathEscape(result.IssueID) + "/triage?slack_link=" + url.QueryEscape(result.LinkToken)
		return "Link your Slack identity to your Gregale account: <" + link + "|Confirm account link>. Open this link yourself; do not share it. Linking does not perform the action. Click the Slack action again after confirming.", nil
	default:
		return "", state.ErrInvalidArgument
	}
}
