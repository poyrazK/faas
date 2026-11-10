package api

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// defaultApprovalPollInterval is how often a waiting client asks whether a
// person has decided an agent approval request.
const defaultApprovalPollInterval = 3 * time.Second

// WaitForApproval makes the client wait for a person to decide an agent
// approval request (ADR-20261104140058 §6). When a request is answered
// 428 approval_required, the client calls notify with the request and its
// review link, polls the request until it is decided or expires, and makes
// the same call once more, with the same body and Idempotency-Key, if it was
// approved. A denied or expired request returns approval_denied or
// approval_expired. Each call waits at most once. It returns the client.
func (c *Client) WaitForApproval(notify func(AgentApprovalPendingInfo)) *Client {
	c.approvalNotify = notify
	c.approvalWait = true
	return c
}

// pendingApproval returns the request behind an approval_required answer.
func pendingApproval(err error) (AgentApprovalPendingInfo, bool) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Problem.Code != CodeApprovalRequired || apiErr.Problem.AgentApproval == nil || apiErr.Problem.AgentApproval.ID == "" {
		return AgentApprovalPendingInfo{}, false
	}
	return *apiErr.Problem.AgentApproval, true
}

// awaitApproval polls an approval request until a person decides it. It
// returns nil once the request is approved, so the caller may retry, and the
// error to report otherwise.
func (c *Client) awaitApproval(ctx context.Context, pending AgentApprovalPendingInfo, original error) error {
	if c.approvalNotify != nil {
		c.approvalNotify(pending)
	}
	if expiresAt, err := time.Parse(time.RFC3339, pending.ExpiresAt); err == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, expiresAt)
		defer cancel()
	}
	interval := c.approvalPoll
	if interval <= 0 {
		interval = defaultApprovalPollInterval
	}
	for {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return approvalExpiredError(pending)
			}
			return errors.Join(original, ctx.Err())
		case <-timer.C:
		}
		got, err := c.GetAgentApproval(ctx, pending.ID)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				return errors.Join(original, err)
			}
			continue // a network blip; keep waiting until the request expires
		}
		switch got.Status {
		case AgentApprovalApproved:
			return nil
		case AgentApprovalDenied:
			return approvalDeniedError(pending, got.Note)
		case AgentApprovalExpired:
			return approvalExpiredError(pending)
		case AgentApprovalConsumed:
			// Another call with the same request used the approval.
			return errors.Join(original, errors.New("the approval was already used by another request; run the command again"))
		}
	}
}

func approvalDeniedError(pending AgentApprovalPendingInfo, note string) error {
	detail := "a person denied this request"
	if note != "" {
		detail += ": " + note
	}
	p := NewProblem(http.StatusForbidden, CodeApprovalDenied, "Approval denied", detail)
	info := pending
	info.Status, info.Note = AgentApprovalDenied, note
	p.AgentApproval = &info
	return &APIError{Problem: *p}
}

func approvalExpiredError(pending AgentApprovalPendingInfo) error {
	p := NewProblem(http.StatusGone, CodeApprovalExpired, "Approval expired", "nobody decided this request before it expired; run the command again to ask anew")
	info := pending
	info.Status = AgentApprovalExpired
	p.AgentApproval = &info
	return &APIError{Problem: *p}
}
