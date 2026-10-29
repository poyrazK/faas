package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// GitHub pull_request deliveries start automations (ADR-20261027090010 §9). Before the
// preview path, githubd builds a bounded event from each delivery and hands
// it to apid's bridge, which publishes it as a gregale.github event of the
// installation's account. The delivery ID is the event ID, so the worker's
// retries publish nothing twice. Without a publisher nothing is forwarded.

// AutomationEvent is one forwarded delivery.
type AutomationEvent struct {
	DeliveryID     string
	InstallationID int64
	Type           string
	// Data is the JSON of an api.GitHubPullRequestEvent.
	Data []byte
}

// AutomationEventPublisher hands forwarded deliveries to apid.
type AutomationEventPublisher interface {
	PublishAutomationEvent(ctx context.Context, event AutomationEvent) error
}

// ErrAutomationEventRejected marks a delivery apid refused for good; the
// delivery goes on without it.
var ErrAutomationEventRejected = errors.New("githubd: automation event rejected")

// gitHubPullRequestWire is the part of a pull_request delivery an automation
// event carries.
type gitHubPullRequestWire struct {
	Action       string              `json:"action"`
	Installation InstallationPayload `json:"installation"`
	Sender       SenderPayload       `json:"sender"`
	Repository   struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	} `json:"repository"`
	PullRequest struct {
		Number  int           `json:"number"`
		Title   string        `json:"title"`
		State   string        `json:"state"`
		Draft   bool          `json:"draft"`
		Merged  bool          `json:"merged"`
		HTMLURL string        `json:"html_url"`
		User    SenderPayload `json:"user"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Head gitHubPullRequestRefWire `json:"head"`
		Base gitHubPullRequestRefWire `json:"base"`
	} `json:"pull_request"`
}

type gitHubPullRequestRefWire struct {
	Ref  string               `json:"ref"`
	SHA  string               `json:"sha"`
	Repo *PullRequestHeadRepo `json:"repo"`
}

// PullRequestAutomationEvent builds the bounded event for a delivery. It
// reports false for actions that start no automation.
func PullRequestAutomationEvent(body []byte) (api.GitHubPullRequestEvent, bool, error) {
	var wire gitHubPullRequestWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return api.GitHubPullRequestEvent{}, false, fmt.Errorf("githubd: decode pull_request: %w", err)
	}
	if !api.GitHubPullRequestEventAction(wire.Action) {
		return api.GitHubPullRequestEvent{}, false, nil
	}
	pr := wire.PullRequest
	event := api.GitHubPullRequestEvent{Action: wire.Action, InstallationID: wire.Installation.ID, Sender: wire.Sender.Login,
		Repository: api.GitHubEventRepository{FullName: wire.Repository.FullName, DefaultBranch: wire.Repository.DefaultBranch, Private: wire.Repository.Private},
		PullRequest: api.GitHubEventPullRequest{Number: pr.Number, Title: truncateUTF8(pr.Title, api.GitHubEventTitleMaxBytes),
			State: pr.State, Draft: pr.Draft, Merged: pr.Merged, URL: pr.HTMLURL, Author: pr.User.Login,
			Head: api.GitHubEventRef{Ref: pr.Head.Ref, SHA: strings.ToLower(pr.Head.SHA)},
			Base: api.GitHubEventRef{Ref: pr.Base.Ref, SHA: strings.ToLower(pr.Base.SHA)}}}
	if pr.Head.Repo != nil {
		event.PullRequest.Head.Repository = pr.Head.Repo.FullName
	}
	// A head repository GitHub no longer names (a deleted fork) counts as a
	// fork: nothing shows it is the base repository.
	event.PullRequest.Fork = !strings.EqualFold(event.PullRequest.Head.Repository, event.Repository.FullName)
	for _, label := range pr.Labels {
		if len(event.PullRequest.Labels) == api.GitHubEventLabelsMax {
			break
		}
		if name := truncateUTF8(label.Name, api.GitHubEventLabelMaxBytes); name != "" {
			event.PullRequest.Labels = append(event.PullRequest.Labels, name)
		}
	}
	return event, true, nil
}

// forwardPullRequest publishes a pull_request delivery as an automation
// event. Only a failure a retry may fix fails the delivery.
func (s *Service) forwardPullRequest(ctx context.Context, body []byte) error {
	if s.AutomationEvents == nil {
		return nil
	}
	deliveryID := strings.ToLower(webhookDeliveryID(ctx))
	event, ok, err := PullRequestAutomationEvent(body)
	if err != nil || !ok || deliveryID == "" {
		if err != nil {
			s.Log.Warn("githubd: pull_request not forwarded to automations", "delivery_id", deliveryID, "err", err)
		}
		return nil
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("githubd: encode automation event: %w", err)
	}
	err = s.AutomationEvents.PublishAutomationEvent(ctx, AutomationEvent{DeliveryID: deliveryID,
		InstallationID: event.InstallationID, Type: event.Type(), Data: data})
	if errors.Is(err, ErrAutomationEventRejected) {
		s.Log.Warn("githubd: apid refused a pull_request automation event", "delivery_id", deliveryID,
			"repo", event.Repository.FullName, "pr_number", event.PullRequest.Number, "err", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("githubd: forward pull_request to automations: %w", err)
	}
	return nil
}

// truncateUTF8 cuts s to at most max bytes at a character boundary.
func truncateUTF8(s string, max int) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
