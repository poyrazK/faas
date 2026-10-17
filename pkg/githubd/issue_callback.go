package githubd

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/issuetracker"
)

// The existing inbox has already verified and durably recorded this delivery.
// githubd only produces a receipt; apid alone schedules/executes reconciliation.
func (s *Service) handleIssueCallback(ctx context.Context, body []byte) error {
	event, err := issuetracker.DecodeGitHubIssueCallback(body, webhookDeliveryID(ctx))
	if errors.Is(err, issuetracker.ErrIgnoredCallback) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.IssueCallbacks == nil {
		return errors.New("tracker callback storage unavailable")
	}
	_, err = s.IssueCallbacks.EnqueueIssueTrackerCallback(ctx, event)
	return err
}
