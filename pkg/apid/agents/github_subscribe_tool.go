package agents

// gregale_github_subscribe (ADR-20261030125501 §4) subscribes the calling
// thread to a repository's gregale.github events. The subscription is a row,
// so a subscribed agent holds nothing while it waits. The call is answered at
// once with a RAP tool result marked as a subscription; each matching event
// githubd forwards later reaches the thread as a RAP subscription_event for
// that call, until the subscription expires or the thread is revoked.
//
// Events reach an account only from its own GitHub installations, so a
// subscription to a repository the account cannot see never fires.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// GitHubSubscriptionStore holds gregale_github_subscribe's subscriptions;
// *agentstore.Store implements it.
type GitHubSubscriptionStore interface {
	SubscribeGitHub(ctx context.Context, sub agentstore.GitHubSubscription, now time.Time, max int) (agentstore.GitHubSubscription, bool, error)
	GitHubSubscribers(ctx context.Context, accountID, repository, eventType string, now time.Time, limit int) ([]agentstore.GitHubSubscription, error)
	DeleteExpiredGitHubSubscriptions(ctx context.Context, before time.Time, limit int) (int64, error)
}

const (
	toolGitHubSubscribe = "gregale_github_subscribe"
	// gitHubSubscribersMax bounds the threads one forwarded event reaches.
	gitHubSubscribersMax = 256
	// Expired subscriptions are deleted a day after they expire, in batches.
	gitHubSubscriptionRetention = 24 * time.Hour
	gitHubSubscriptionSweepRows = 500
)

// gitHubPullRequestActions are the pull_request actions githubd forwards
// (api.GitHubPullRequestEventAction), in a stable order.
var gitHubPullRequestActions = []string{"opened", "reopened", "synchronize", "ready_for_review", "closed"}

// errToolNeedsCaller refuses a gregale_github_subscribe invocation that
// carries only the callback URL: a subscription reads the account's GitHub
// events.
var errToolNeedsCaller = api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "App credential required",
	"gregale_github_subscribe reads the account's GitHub events, so its invocation needs the app's gregale:agents workload token or an API key with deploy:write")

func githubSubscribeTool() rapTool {
	return rapTool{
		Name: toolGitHubSubscribe,
		Description: "Subscribe this thread to a GitHub repository's pull request events, for at most 30 days. " +
			"Each event arrives later as a subscription event of this call, with the pull request's number, title, state, refs and URL. " +
			"The repository must be in one of the account's GitHub installations. The invocation needs the app's credential.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repository": map[string]any{"type": "string", "description": "The repository, as owner/name"},
				"events": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": gitHubPullRequestActions},
					"description": "The pull_request actions to receive; all of them when omitted"},
				"until": map[string]any{"type": "string", "format": "date-time", "description": "RFC 3339 time the subscription ends; 30 days ahead when omitted"},
			},
			"required":             []string{"repository"},
			"additionalProperties": false,
		},
		DisplayScript: `"Watching " + args.repository`,
	}
}

type gitHubSubscribeArguments struct {
	Repository string   `json:"repository"`
	Events     []string `json:"events"`
	Until      string   `json:"until"`
}

// subscribeGitHub records the call's subscription and answers it. Refusals
// the agent can act on are its tool result; a store failure is a 5xx, which
// the runtime retries.
func (h *Handler) subscribeGitHub(ctx context.Context, grant CallbackToken, inv rapInvocation, caller *CallbackCaller) *api.Problem {
	if caller == nil {
		return errToolNeedsCaller
	}
	if h.GitHubSubscriptions == nil {
		return api.ErrCapacity("GitHub subscriptions are unavailable on this control-plane host")
	}
	now := h.now()
	sub, why := gitHubSubscription(grant, inv, now)
	if why != "" {
		return h.answerTool(ctx, grant, inv, toolGitHubSubscribe+": "+why)
	}
	sub, _, err := h.GitHubSubscriptions.SubscribeGitHub(ctx, sub, now, api.AgentGitHubSubscriptionsPerEndpoint)
	var limit *agentstore.LimitError
	if errors.As(err, &limit) {
		return h.answerTool(ctx, grant, inv, fmt.Sprintf("%s: this inbox has %d live subscriptions, the most it may have (%s)",
			toolGitHubSubscribe, limit.Observed, limit.Limit))
	}
	if err != nil {
		return h.problem(caller.Account, "agent_github_subscription.create", err)
	}
	text := fmt.Sprintf("Subscribed to %s on %s until %s. Each event arrives as a subscription event of this call.",
		strings.Join(sub.EventTypes, ", "), sub.Repository, sub.ExpiresAt.UTC().Format(time.RFC3339))
	post, problem := subscriptionResultPost(grant, inv, text)
	if problem == nil {
		_, problem = h.publish(ctx, grant, post)
	}
	return problem
}

// gitHubSubscription reads the call's arguments into its subscription, or
// says what is wrong with them.
func gitHubSubscription(grant CallbackToken, inv rapInvocation, now time.Time) (agentstore.GitHubSubscription, string) {
	var args gitHubSubscribeArguments
	dec := json.NewDecoder(bytes.NewReader(inv.Arguments))
	dec.DisallowUnknownFields()
	if dec.Decode(&args) != nil {
		return agentstore.GitHubSubscription{}, "arguments must be an object with a repository, and optionally events and until"
	}
	repository := strings.ToLower(strings.TrimSpace(args.Repository))
	if !api.ValidAgentRepository(repository) {
		return agentstore.GitHubSubscription{}, "repository must be owner/name"
	}
	actions := args.Events
	if len(actions) == 0 {
		actions = gitHubPullRequestActions
	}
	var types []string
	for _, action := range actions {
		if !api.GitHubPullRequestEventAction(action) {
			return agentstore.GitHubSubscription{}, fmt.Sprintf("events must be pull_request actions: %s", strings.Join(gitHubPullRequestActions, ", "))
		}
		if t := api.GitHubPullRequestEventPrefix + action; !slices.Contains(types, t) {
			types = append(types, t)
		}
	}
	latest := now.Add(time.Duration(api.AgentGitHubSubscriptionMaxSeconds) * time.Second)
	expires := latest
	if args.Until != "" {
		until, err := time.Parse(time.RFC3339, args.Until)
		if err != nil || !until.After(now) || until.After(latest) {
			return agentstore.GitHubSubscription{}, "until must be an RFC 3339 time in the next 30 days"
		}
		expires = until
	}
	return agentstore.GitHubSubscription{AccountID: grant.AccountID, EndpointID: grant.EndpointID, ThreadID: grant.ThreadID,
		ToolCallID: inv.ID, Repository: repository, EventTypes: types, IssuedAt: grant.IssuedAt, ExpiresAt: expires.UTC()}, ""
}

// subscriptionResultPost is the call's tool result, marked as a
// subscription: the runtime then expects subscription events for the call.
func subscriptionResultPost(grant CallbackToken, inv rapInvocation, text string) (api.AgentCallbackPost, *api.Problem) {
	subscription := true
	data, err := json.Marshal(rapToolResultMessage{Type: rapToolResult, GroupID: grant.ThreadID, ID: inv.ID, CallID: inv.CallID, Text: text,
		Subscription: &subscription})
	if err != nil {
		return api.AgentCallbackPost{}, api.ErrValidation("the tool result is not encodable")
	}
	post := api.AgentCallbackPost{MessageID: rapMessageID("rap-tool-result-", inv.ID), Kind: api.AgentCallbackKindToolResult, Data: data}
	return post, checkCallbackPost(grant, post)
}

// rapSubscriptionEventMessage is a RAP subscription event (RAP spec,
// "Subscription Event").
type rapSubscriptionEventMessage struct {
	Type       string `json:"type"`
	GroupID    string `json:"group_id"`
	ToolCallID string `json:"tool_call_id"`
	Text       string `json:"text"`
}

// gitHubSubscriptionEvent is a subscription event's text: the event, which
// carries no pull request body or diff.
type gitHubSubscriptionEvent struct {
	Type       string                     `json:"type"`
	DeliveryID string                     `json:"delivery_id"`
	Event      api.GitHubPullRequestEvent `json:"event"`
}

// DeliverGitHubEvent publishes one forwarded GitHub event to every thread
// subscribed to its repository and type. githubd retries a delivery that
// returns an error, and each thread's message ID derives from the delivery
// and the subscription, so a retried delivery lands once per thread. A
// thread that can no longer receive it gets nothing.
func (h *Handler) DeliverGitHubEvent(ctx context.Context, accountID, deliveryID string, event api.GitHubPullRequestEvent) error {
	if h.GitHubSubscriptions == nil || !h.callbacksConfigured() {
		return nil
	}
	subs, err := h.GitHubSubscriptions.GitHubSubscribers(ctx, accountID, strings.ToLower(event.Repository.FullName), event.Type(),
		h.now(), gitHubSubscribersMax)
	if err != nil {
		return err
	}
	text, err := json.Marshal(gitHubSubscriptionEvent{Type: event.Type(), DeliveryID: deliveryID, Event: event})
	if err != nil {
		return fmt.Errorf("encode github subscription event: %w", err)
	}
	var errs []error
	for _, sub := range subs {
		if err := h.deliverSubscriptionEvent(ctx, sub, deliveryID, string(text)); err != nil {
			errs = append(errs, fmt.Errorf("subscription %s: %w", sub.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (h *Handler) deliverSubscriptionEvent(ctx context.Context, sub agentstore.GitHubSubscription, deliveryID, text string) error {
	grant := CallbackToken{AccountID: sub.AccountID, EndpointID: sub.EndpointID, ThreadID: sub.ThreadID,
		Kinds: []string{api.AgentCallbackKindEvent}, IssuedAt: sub.IssuedAt}
	live, err := h.threadReceives(ctx, grant)
	if err != nil || !live {
		return err
	}
	data, err := json.Marshal(rapSubscriptionEventMessage{Type: rapSubscriptionEvent, GroupID: sub.ThreadID, ToolCallID: sub.ToolCallID, Text: text})
	if err != nil {
		return fmt.Errorf("encode subscription event: %w", err)
	}
	post := api.AgentCallbackPost{MessageID: rapMessageID("gh-", deliveryID+"-"+sub.ID), Kind: api.AgentCallbackKindEvent, Data: data}
	if problem := checkCallbackPost(grant, post); problem != nil {
		// The event is bounded, so this is a bug; retrying cannot fix it.
		if h.Log != nil {
			h.Log.Warn("agent github subscription: event not publishable", "subscription_id", sub.ID, "detail", problem.Detail)
		}
		return nil
	}
	_, err = h.Inbox.AppendManagedRealtimeChannelMetadata(ctx, grant.EndpointID, api.AgentInboxChannel, post.Data, false,
		callbackIdempotencyKey(grant.ThreadID, post.MessageID), callbackMetadata(grant.ThreadID, post))
	// Not found: the endpoint went away since the read. A conflict cannot
	// happen for one delivery and subscription; either way, nothing to retry.
	if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
		return fmt.Errorf("publish subscription event: %w", err)
	}
	return nil
}

// sweepGitHubSubscriptions deletes subscriptions a day past their expiry.
func (h *Handler) sweepGitHubSubscriptions(ctx context.Context) {
	if h.GitHubSubscriptions == nil {
		return
	}
	if _, err := h.GitHubSubscriptions.DeleteExpiredGitHubSubscriptions(ctx, h.now().Add(-gitHubSubscriptionRetention),
		gitHubSubscriptionSweepRows); err != nil && ctx.Err() == nil && h.Log != nil {
		h.Log.Warn("agent github subscriptions sweep", "err", err)
	}
}
