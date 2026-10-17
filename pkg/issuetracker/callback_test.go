package issuetracker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func callbackTestSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
func TestLinearCallbackVerifiesRawBodyScopeAndRetryIdentity(t *testing.T) {
	secret := "linear_signing_secret_1234567890abcdef"
	scope := LinearIdentity{WorkspaceID: uuid.NewString(), TeamID: uuid.NewString(), ViewerID: uuid.NewString()}
	app, delivery, ticket := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Now()
	payload := map[string]any{"type": "Issue", "action": "update", "organizationId": scope.WorkspaceID, "webhookTimestamp": now.UnixMilli(), "data": map[string]string{"id": ticket, "teamId": scope.TeamID, "updatedAt": "same event"}}
	body, _ := json.Marshal(payload)
	event, err := DecodeLinearIssueCallback(body, callbackTestSignature(body, secret), delivery, secret, scope, app, 7)
	if err != nil || !event.Fresh(now) || event.TicketID != ticket || event.ConnectionRevision != 7 {
		t.Fatal(event, err)
	}
	payload["webhookTimestamp"] = now.Add(2 * time.Minute).UnixMilli()
	retry, _ := json.Marshal(payload)
	other, err := DecodeLinearIssueCallback(retry, callbackTestSignature(retry, secret), delivery, secret, scope, app, 7)
	if err != nil || other.PayloadHash != event.PayloadHash || other.Fresh(now) {
		t.Fatal("retry timestamp changed event identity or bypassed freshness", other, err)
	}
	if _, err := DecodeLinearIssueCallback(retry, callbackTestSignature(body, secret), delivery, secret, scope, app, 7); err == nil {
		t.Fatal("raw-body tampering accepted")
	}
	for _, kind := range []string{"signature", "workspace", "team", "delivery", "missing timestamp", "short secret"} {
		t.Run(kind, func(t *testing.T) {
			copy := map[string]any{"type": "Issue", "action": "update", "organizationId": scope.WorkspaceID, "webhookTimestamp": now.UnixMilli(), "data": map[string]string{"id": ticket, "teamId": scope.TeamID}}
			d, key := delivery, secret
			switch kind {
			case "workspace":
				copy["organizationId"] = uuid.NewString()
			case "team":
				copy["data"] = map[string]string{"id": ticket, "teamId": uuid.NewString()}
			case "delivery":
				d = "invalid"
			case "missing timestamp":
				delete(copy, "webhookTimestamp")
			case "short secret":
				key = "short"
			}
			b, _ := json.Marshal(copy)
			sig := callbackTestSignature(b, key)
			if kind == "signature" {
				sig = "bad"
			}
			_, err := DecodeLinearIssueCallback(b, sig, d, key, scope, app, 7)
			if !errors.Is(err, ErrInvalidCallback) {
				t.Fatal("invalid callback accepted", err)
			}
			want := "invalid_payload"
			if kind == "signature" {
				want = "invalid_signature"
			}
			if kind == "workspace" || kind == "team" {
				want = "wrong_scope"
			}
			if CallbackRejectionReason(err) != want {
				t.Fatal("wrong rejection classification", err, want)
			}

		})
	}
}
func TestGitHubIssueCallbackRequiresMatchingImmutableRepositoryAndCanonicalTicket(t *testing.T) {
	body := []byte(`{"action":"closed","installation":{"id":42},"repository":{"id":100,"full_name":"Owner/Repo"},"issue":{"number":42,"html_url":"https://github.com/Owner/Repo/issues/42"}}`)
	event, err := DecodeGitHubIssueCallback(body, "delivery")
	if err != nil || event.InstallationID != 42 || event.RepositoryID != 100 || event.TicketURL != "https://github.com/owner/repo/issues/42" {
		t.Fatal(event, err)
	}
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	payload["issue"].(map[string]any)["html_url"] = "https://github.com/foreign/repo/issues/42"
	bad, _ := json.Marshal(payload)
	if _, err := DecodeGitHubIssueCallback(bad, "delivery"); err == nil {
		t.Fatal("foreign ticket accepted")
	}
	payload["issue"].(map[string]any)["html_url"] = event.TicketURL
	payload["issue"].(map[string]any)["pull_request"] = map[string]string{"url": "pull"}
	pull, _ := json.Marshal(payload)
	if _, err := DecodeGitHubIssueCallback(pull, "delivery"); !errors.Is(err, ErrIgnoredCallback) {
		t.Fatal("pull request treated as issue", err)
	}
}
