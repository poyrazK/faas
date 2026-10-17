package issueslack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSlackInteractionSignatureAndPayload(t *testing.T) {
	secret := strings.Repeat("a", 32)
	now := time.Unix(1800000000, 0)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	p := map[string]any{"type": "block_actions", "api_app_id": "A0123456789", "team": map[string]string{"id": "T0123456789"}, "user": map[string]string{"id": "U0123456789"}, "channel": map[string]string{"id": "C0123456789"}, "container": map[string]string{"type": "message", "channel_id": "C0123456789", "message_ts": "123.456"}, "response_url": "https://hooks.slack.com/actions/T0123456789/123456789/test-response-token", "actions": []map[string]string{{"action_id": "gregale_acknowledge", "value": uuid.NewString(), "action_ts": "124.456"}}}
	encode := func() []byte { b, _ := json.Marshal(p); return []byte(url.Values{"payload": {string(b)}}.Encode()) }
	raw := encode()
	sign := func(ts string, body []byte) string {
		h := hmac.New(sha256.New, []byte(secret))
		h.Write([]byte("v0:" + ts + ":"))
		h.Write(body)
		return "v0=" + hex.EncodeToString(h.Sum(nil))
	}
	sig := sign(timestamp, raw)
	if !VerifyInteraction(secret, timestamp, sig, raw, now) {
		t.Fatal("valid signature rejected")
	}
	for _, tc := range []struct {
		ts, sig string
		raw     []byte
	}{
		{timestamp, sig, append(append([]byte{}, raw...), 'x')},
		{timestamp, "v0=" + strings.Repeat("0", 64), raw},
		{"invalid", sig, raw},
		{strconv.FormatInt(now.Add(-api.IssueSlackSignatureMaxAge).Unix(), 10), "", raw},
		{strconv.FormatInt(now.Add(api.IssueSlackSignatureMaxAge).Unix(), 10), "", raw},
	} {
		if tc.sig == "" {
			tc.sig = sign(tc.ts, tc.raw)
		}
		if VerifyInteraction(secret, tc.ts, tc.sig, tc.raw, now) {
			t.Fatal("invalid signature accepted")
		}
	}
	if api.IssueSlackLinkTTL <= 2*api.IssueSlackSignatureMaxAge-time.Second {
		t.Fatal("link receipts must outlive the full signature replay window")
	}
	policy := state.IssueSlackInteractionPolicy{Revision: 4, WorkspaceID: "T0123456789", SlackAppID: "A0123456789"}
	in, err := ParseInteraction(raw, policy)
	if err != nil || in.UserID != "U0123456789" || in.Action != "acknowledge" || len(in.RequestKey) != 64 || in.PolicyRevision != 4 {
		t.Fatal(in, err)
	}
	again, err := ParseInteraction(raw, policy)
	if err != nil || in.RequestKey != again.RequestKey {
		t.Fatal("unstable retry identity")
	}
	for _, tc := range []struct{ id, action string }{
		{"gregale_assign_to_me", "assign_to_me"},
		{"gregale_acknowledge", "acknowledge"},
		{"gregale_snooze_1h", "snooze_1h"},
		{"gregale_snooze_24h", "snooze_24h"},
		{"gregale_assign_someone_else", ""},
	} {
		p["actions"].([]map[string]string)[0]["action_id"] = tc.id
		action, err := ParseInteraction(encode(), policy)
		if tc.action == "" {
			if err == nil {
				t.Fatal("unsupported assignment accepted")
			}
			continue
		}
		if err != nil || action.Action != tc.action || action.DeliveryID != in.DeliveryID || (tc.action != in.Action && action.RequestKey == in.RequestKey) {
			t.Fatal("action/message binding", action, err)
		}
	}
	p["actions"].([]map[string]string)[0]["action_id"] = "gregale_acknowledge"
	for _, field := range []string{"type", "api_app_id", "team", "user", "container", "actions"} {
		old := p[field]
		p[field] = nil
		if _, err := ParseInteraction(encode(), policy); err == nil {
			t.Fatal("invalid field accepted", field)
		}
		p[field] = old
	}
	policy.WorkspaceID = "T9876543210"
	if _, err := ParseInteraction(raw, policy); err == nil {
		t.Fatal("workspace mismatch")
	}
	if VerifyInteraction("short", timestamp, sig, raw, now) {
		t.Fatal("weak secret")
	}
}
func TestNativeSlackButtonsAndSafeLinkConfirmation(t *testing.T) {
	for _, kind := range []string{"created", "application_reminder", "application_escalation"} {
		c := state.IssueSlackClaim{Application: true, Interactive: true, CanAssign: true, Kind: kind, ID: uuid.NewString(), AppSlug: "app", IssueID: uuid.NewString(), ChannelID: "C0123456789"}
		body, err := message(c, "https://gregale.dev")
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Blocks []struct {
				Type     string                       `json:"type"`
				Elements []struct{ ID, Value string } `json:"elements"`
			}
		}
		if json.Unmarshal(body, &p) != nil {
			t.Fatal("message shape")
		}
		n := 0
		for _, block := range p.Blocks {
			if block.Type == "actions" {
				n = len(block.Elements)
				for _, e := range block.Elements {
					if e.Value != c.ID {
						t.Fatal("button context")
					}
				}
			}
		}
		if n != 4 || !strings.Contains(string(body), "gregale_assign_to_me") || !strings.Contains(string(body), "gregale_acknowledge") || !strings.Contains(string(body), "gregale_snooze_24h") {
			t.Fatal(string(body))
		}
		c.CanAssign = false
		legacy, err := message(c, "https://gregale.dev")
		if err != nil || strings.Contains(string(legacy), "gregale_assign_to_me") {
			t.Fatal("legacy snapshot gained assignment", err)
		}
		c.Interactive = false
		legacy, err = message(c, "https://gregale.dev")
		if err != nil || strings.Contains(string(legacy), "\"actions\"") {
			t.Fatal("noninteractive message gained buttons", err)
		}
	}
	result := state.IssueSlackInteractionResult{Status: "link_required", AppSlug: "app", IssueID: "issue", LinkToken: strings.Repeat("a", 64)}
	text, err := InteractionText(result, "https://gregale.dev")
	if err != nil || !strings.Contains(text, "does not perform the action") || !strings.Contains(text, "slack_link=") {
		t.Fatal(text, err)
	}
	for _, base := range []string{"http://gregale.dev", "https://evil@gregale.dev", "https://gregale.dev?next=evil", "https://gregale.dev#evil"} {
		if _, err := InteractionText(result, base); err == nil {
			t.Fatal(base)
		}
	}
	text, err = InteractionText(state.IssueSlackInteractionResult{Status: "assigned"}, "https://gregale.dev")
	if err != nil || !strings.Contains(text, "assigned to you") || !strings.Contains(text, "unchanged") {
		t.Fatal("assignment confirmation", text, err)
	}
}
