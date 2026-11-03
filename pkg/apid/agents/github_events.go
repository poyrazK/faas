package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state"
)

// GitHub pull request events start automations (ADR-20261027090010 §9). githubd's
// delivery worker hands apid each pull_request delivery it forwards; apid
// publishes it as a gregale.github platform event of the installation's
// account. The events trigger captures the account's workflows whose event
// trigger matches, so one of them can run an agent step on the pull request.
// Only accounts the agents capability is released to receive the events.

// Reasons a GitHub event is not published.
const (
	GitHubEventSkippedNoAccount  = "no_account"
	GitHubEventSkippedNotEnabled = "not_enabled"
)

// ErrGitHubEventConflict means the delivery ID already names a different event.
var ErrGitHubEventConflict = errors.New("github event: delivery already published with different content")

// GitHubEventStore resolves the installation's account and accepts the event.
type GitHubEventStore interface {
	state.GitHubEventStore
	AccountByID(ctx context.Context, id string) (state.Account, error)
}

// GitHubEvents publishes githubd's forwarded deliveries.
type GitHubEvents struct {
	Store GitHubEventStore
	// Allowed decides the agents capability; nil publishes nothing.
	Allowed func(state.Account) bool
	// Notify wakes schedd's event fanout after a new event.
	Notify func(context.Context)
	Now    func() time.Time
}

// GitHubEventResult says what one delivery published.
type GitHubEventResult struct {
	AccountID string
	Published bool
	Duplicate bool
	Skipped   string
}

// Publish publishes one delivery. A repeated delivery publishes nothing
// twice: the delivery ID is the event ID.
func (g *GitHubEvents) Publish(ctx context.Context, deliveryID string, installationID int64, eventType string, data []byte) (GitHubEventResult, error) {
	event, err := decodeGitHubEvent(deliveryID, installationID, eventType, data)
	if err != nil {
		return GitHubEventResult{}, err
	}
	accountID, err := g.Store.AccountIDByGitHubInstallation(ctx, installationID)
	if errors.Is(err, state.ErrNotFound) {
		return GitHubEventResult{Skipped: GitHubEventSkippedNoAccount}, nil
	}
	if err != nil {
		return GitHubEventResult{}, fmt.Errorf("resolve installation account: %w", err)
	}
	acct, err := g.Store.AccountByID(ctx, accountID)
	if errors.Is(err, state.ErrNotFound) {
		return GitHubEventResult{Skipped: GitHubEventSkippedNoAccount}, nil
	}
	if err != nil {
		return GitHubEventResult{}, fmt.Errorf("read installation account: %w", err)
	}
	if !acct.Active() || g.Allowed == nil || !g.Allowed(acct) {
		return GitHubEventResult{AccountID: acct.ID, Skipped: GitHubEventSkippedNotEnabled}, nil
	}
	payload, err := gitHubEventPayload(acct.ID, deliveryID, event, g.now())
	if err != nil {
		return GitHubEventResult{}, err
	}
	accepted, err := g.Store.AcceptPublishedEvent(ctx, "githubd", acct.ID, payload)
	if errors.Is(err, state.ErrConflict) {
		return GitHubEventResult{}, ErrGitHubEventConflict
	}
	if err != nil {
		return GitHubEventResult{}, fmt.Errorf("publish github event: %w", err)
	}
	if !accepted.Duplicate && g.Notify != nil {
		g.Notify(ctx)
	}
	return GitHubEventResult{AccountID: acct.ID, Published: true, Duplicate: accepted.Duplicate}, nil
}

func (g *GitHubEvents) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// decodeGitHubEvent reads the bounded event githubd built and checks it
// matches the request around it.
func decodeGitHubEvent(deliveryID string, installationID int64, eventType string, data []byte) (api.GitHubPullRequestEvent, error) {
	var event api.GitHubPullRequestEvent
	if id, err := uuid.Parse(deliveryID); err != nil || id.String() != deliveryID {
		return event, fmt.Errorf("%w: delivery_id must be a lowercase GUID", api.ErrGitHubEventInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return event, fmt.Errorf("%w: data: %w", api.ErrGitHubEventInvalid, err)
	}
	if err := event.Validate(); err != nil {
		return event, err
	}
	if event.InstallationID != installationID || event.Type() != eventType {
		return event, fmt.Errorf("%w: data does not match the installation or event type", api.ErrGitHubEventInvalid)
	}
	return event, nil
}

func gitHubEventPayload(accountID, deliveryID string, event api.GitHubPullRequestEvent, now time.Time) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	envelope, err := (eventcontract.Envelope{ID: deliveryID, Source: api.GitHubEventSource, Type: event.Type(), Data: data}).Normalize(accountID, now)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", api.ErrGitHubEventInvalid, err)
	}
	return json.Marshal(envelope)
}
