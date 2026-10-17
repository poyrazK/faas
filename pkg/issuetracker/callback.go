package issuetracker

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrIgnoredCallback = errors.New("tracker callback is not an issue change")
var ErrInvalidCallback = errors.New("tracker callback could not be verified")

type CallbackEvent struct {
	Provider, ScopeKey, DeliveryID, AppID, WorkspaceID, TeamID, TicketID, TicketURL, PayloadHash string
	InstallationID, RepositoryID, ConnectionRevision                                             int64
	SentAt                                                                                       time.Time
}

func validDelivery(id string) bool {
	return len(id) > 0 && len(id) <= api.IssueTrackerCallbackDeliveryMaxBytes && !strings.ContainsFunc(id, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}
func callbackHash(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

// DecodeGitHubIssueCallback is called only by the authenticated githubd inbox.
// The event is a refresh hint, never an authoritative issue lifecycle update.
func DecodeGitHubIssueCallback(body []byte, delivery string) (CallbackEvent, error) {
	if len(body) > api.IssueTrackerCallbackBodyMaxBytes || !validDelivery(delivery) {
		return CallbackEvent{}, ErrInvalidCallback
	}
	var event struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		} `json:"repository"`
		Issue struct {
			Number      int64           `json:"number"`
			URL         string          `json:"html_url"`
			PullRequest json.RawMessage `json:"pull_request"`
		} `json:"issue"`
	}
	if json.Unmarshal(body, &event) != nil {
		return CallbackEvent{}, ErrInvalidCallback
	}
	if event.Action == "" || event.Installation.ID <= 0 || event.Repository.ID <= 0 || event.Issue.Number <= 0 {
		return CallbackEvent{}, ErrInvalidCallback
	}
	if len(event.Issue.PullRequest) > 0 && string(event.Issue.PullRequest) != "null" {
		return CallbackEvent{}, ErrIgnoredCallback
	}
	normalized, err := api.NormalizeIssueTrackerLink(api.PutIssueTrackerLinkRequest{URL: event.Issue.URL})
	if err != nil || normalized.Provider != "github" || !strings.EqualFold(normalized.ExternalKey, event.Repository.FullName+"#"+strconv.FormatInt(event.Issue.Number, 10)) {
		return CallbackEvent{}, ErrInvalidCallback
	}
	return CallbackEvent{Provider: "github", ScopeKey: fmt.Sprintf("github/%d", event.Installation.ID), DeliveryID: delivery, InstallationID: event.Installation.ID, RepositoryID: event.Repository.ID, TicketID: strconv.FormatInt(event.Issue.Number, 10), TicketURL: normalized.URL, PayloadHash: callbackHash(body)}, nil
}

// DecodeLinearIssueCallback verifies the raw-body HMAC before reading scope.
// Timestamp freshness is checked by the receiver after known-retry lookup.
func DecodeLinearIssueCallback(body []byte, signature, delivery, secret string, identity LinearIdentity, app string, revision int64) (CallbackEvent, error) {
	if len(body) > api.IssueTrackerCallbackBodyMaxBytes || !ValidLinearWebhookSecret(secret) || revision <= 0 {
		return CallbackEvent{}, callbackRejection("invalid_payload")
	}
	got, err := hex.DecodeString(signature)
	if err != nil || len(got) != sha256.Size {
		return CallbackEvent{}, callbackRejection("invalid_signature")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return CallbackEvent{}, callbackRejection("invalid_signature")
	}
	id, err := uuid.Parse(delivery)
	if err != nil || id == uuid.Nil || id.String() != delivery || id.Version() != 4 {
		return CallbackEvent{}, callbackRejection("invalid_payload")
	}
	var event struct {
		Type           string `json:"type"`
		Action         string `json:"action"`
		OrganizationID string `json:"organizationId"`
		Timestamp      int64  `json:"webhookTimestamp"`
		Data           struct {
			ID     string `json:"id"`
			TeamID string `json:"teamId"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &event) != nil || event.Timestamp <= 0 {
		return CallbackEvent{}, callbackRejection("invalid_payload")
	}
	if event.OrganizationID != identity.WorkspaceID {
		return CallbackEvent{}, callbackRejection("wrong_scope")
	}
	if event.Type != "Issue" {
		return CallbackEvent{}, ErrIgnoredCallback
	}
	if event.Action != "create" && event.Action != "update" && event.Action != "remove" {
		return CallbackEvent{}, ErrIgnoredCallback
	}
	ticket, e := uuid.Parse(event.Data.ID)
	if e != nil || ticket == uuid.Nil || ticket.String() != event.Data.ID {
		return CallbackEvent{}, callbackRejection("invalid_payload")
	}
	if event.Data.TeamID != identity.TeamID {
		return CallbackEvent{}, callbackRejection("wrong_scope")
	}
	// Linear may refresh the sending timestamp when retrying the same delivery.
	// Hash the canonical signed payload excluding only that transport timestamp.
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		return CallbackEvent{}, callbackRejection("invalid_payload")
	}
	delete(payload, "webhookTimestamp")
	canonical, err := json.Marshal(payload)
	if err != nil {
		return CallbackEvent{}, callbackRejection("invalid_payload")
	}
	return CallbackEvent{Provider: "linear", ScopeKey: fmt.Sprintf("linear/%s/%d", app, revision), DeliveryID: delivery, AppID: app, ConnectionRevision: revision, WorkspaceID: identity.WorkspaceID, TeamID: identity.TeamID, TicketID: ticket.String(), PayloadHash: callbackHash(canonical), SentAt: time.UnixMilli(event.Timestamp)}, nil
}
func ValidLinearWebhookSecret(secret string) bool {
	return len(secret) >= api.IssueTrackerCallbackSecretMinBytes && len(secret) <= api.IssueTrackerCredentialMaxBytes && !strings.ContainsFunc(secret, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}
func (e CallbackEvent) Fresh(now time.Time) bool {
	return !e.SentAt.Before(now.Add(-api.IssueTrackerCallbackTimestampSkew)) && !e.SentAt.After(now.Add(api.IssueTrackerCallbackTimestampSkew))
}
