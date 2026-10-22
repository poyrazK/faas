package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CommitSubscriberWaitResult describes the original acceptance recipient only.
// Generic replay and historical backfill executions are separate observations.
type CommitSubscriberWaitResult struct {
	Publication    CommitEventPublication `json:"publication"`
	SubscriptionID string                 `json:"subscription_id"`
	Status         string                 `json:"status"`
	Recipient      json.RawMessage        `json:"recipient,omitempty"`
}

type commitWaitRecipient struct {
	SubscriptionID       string          `json:"subscription_id"`
	Origin               string          `json:"origin"`
	WorkflowName         string          `json:"workflow_name"`
	ExecutionUnavailable string          `json:"execution_unavailable"`
	Cancellation         json.RawMessage `json:"cancellation"`
	Routing              struct {
		State string `json:"state"`
	} `json:"routing"`
	Execution *struct {
		State string `json:"state"`
	} `json:"execution"`
}

func commitSubscriberStatus(raw json.RawMessage, subscription string) (string, bool, error) {
	var row commitWaitRecipient
	if err := json.Unmarshal(raw, &row); err != nil {
		return "", false, err
	}
	if !strings.EqualFold(row.SubscriptionID, subscription) || (row.Origin != "" && row.Origin != "acceptance") {
		return "", false, nil
	}
	if row.WorkflowName != "" {
		return "unsupported_recipient", true, nil
	}
	if row.Execution != nil {
		switch row.Execution.State {
		case "completed", "failed", "dead_letter", "cancelled", "expired", "superseded":
			return row.Execution.State, true, nil
		case "pending", "dispatching":
			return "pending", true, nil
		default:
			return "unavailable", true, nil
		}
	}
	if len(row.Cancellation) > 0 && string(row.Cancellation) != "null" {
		return "cancelled", true, nil
	}
	if row.ExecutionUnavailable != "" {
		return "unavailable", true, nil
	}
	switch row.Routing.State {
	case "filtered":
		return "filtered", true, nil
	case "failed":
		return "routing_failed", true, nil
	case "pending", "processing":
		return "pending", true, nil
	default:
		return "unavailable", true, nil
	}
}

// WaitCommitSubscriber polls guarded evidence for one original application subscriber.
// Only completed proves original execution success. It never requests replay or cancellation.
// On cancellation/deadline it returns the last observation and the context error.
func (c *Client) WaitCommitSubscriber(ctx context.Context, publication CommitEventPublication, subscription string, interval time.Duration) (CommitSubscriberWaitResult, error) {
	out := CommitSubscriberWaitResult{Publication: publication, SubscriptionID: subscription, Status: "pending"}
	if !commitSubscriberUUID.MatchString(subscription) || subscription == "00000000-0000-0000-0000-000000000000" || interval < 100*time.Millisecond || interval > time.Minute {
		return out, ErrValidation("invalid subscriber wait options")
	}
	subscription = strings.ToLower(subscription)
	out.SubscriptionID = subscription
	for {
		after := ""
		seen := map[string]bool{}
		found := false
		for {
			page, err := c.GetCommitPublicationReceipt(ctx, publication, after, AppEventPublishStatusRecipientsMax)
			if err != nil {
				if ctx.Err() != nil {
					return commitSubscriberInterrupted(out, ctx.Err())
				}
				var apiErr *APIError
				if errors.As(err, &apiErr) && apiErr.Problem.Status == 404 {
					out.Status = "unavailable"
					return out, nil
				}
				if errors.As(err, &apiErr) && apiErr.Problem.Status == 409 {
					out.Status = "acceptance_replaced"
					return out, nil
				}
				return out, err
			}
			if page.EventSource != publication.Source || page.EventID != publication.ID || !page.AcceptedAt.Equal(publication.AcceptedAt) {
				return out, fmt.Errorf("publication evidence identity mismatch")
			}
			for _, recipient := range page.Recipients {
				raw, err := json.Marshal(recipient)
				if err != nil {
					return out, err
				}
				status, match, err := commitSubscriberStatus(raw, subscription)
				if err != nil {
					return out, err
				}
				if match {
					out.Status = status
					out.Recipient = raw
					found = true
					break
				}
			}
			if found {
				break
			}
			if page.NextAfter == "" {
				if page.SnapshotCaptured && page.RoutingSettledAt != nil {
					out.Status = "not_targeted"
					return out, nil
				}
				break
			}
			if seen[page.NextAfter] {
				return out, fmt.Errorf("repeated publication recipient cursor")
			}
			seen[page.NextAfter] = true
			after = page.NextAfter
		}
		if found && out.Status != "pending" {
			return out, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return commitSubscriberInterrupted(out, ctx.Err())
		case <-timer.C:
		}
	}
}

func commitSubscriberInterrupted(out CommitSubscriberWaitResult, err error) (CommitSubscriberWaitResult, error) {
	out.Status = "interrupted"
	if errors.Is(err, context.DeadlineExceeded) {
		out.Status = "timed_out"
	}
	return out, fmt.Errorf("subscriber wait interrupted: %w", err)
}

var commitSubscriberUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
