package apidserver

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/db"
)

// PublishGitHubEvent publishes a pull_request delivery githubd forwards as a
// gregale.github event of the installation's account (ADR-20261027090010 §9). The
// agents capability gates it, like agent steps.
func (b *githubdBridge) PublishGitHubEvent(ctx context.Context, req *githubdpb.PublishGitHubEventRequest) (*githubdpb.PublishGitHubEventResponse, error) {
	store, ok := b.store.(agents.GitHubEventStore)
	if !ok {
		return nil, status.Error(codes.Unavailable, "github events need the Postgres store")
	}
	events := agents.GitHubEvents{Store: store, Allowed: b.agentSteps, Notify: b.notifyEventPublished}
	result, err := events.Publish(ctx, req.GetDeliveryId(), req.GetInstallationId(), req.GetEventType(), req.GetData())
	switch {
	case errors.Is(err, api.ErrGitHubEventInvalid):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, agents.ErrGitHubEventConflict):
		return nil, status.Error(codes.AlreadyExists, err.Error())
	case err != nil:
		b.log.Warn("githubd bridge: publish github event", "delivery_id", req.GetDeliveryId(), "err", err)
		return nil, status.Error(codes.Unavailable, "github event could not be published")
	}
	b.log.Info("githubd bridge: github event", "delivery_id", req.GetDeliveryId(), "type", req.GetEventType(),
		"account_id", result.AccountID, "published", result.Published, "duplicate", result.Duplicate, "skipped", result.Skipped)
	return &githubdpb.PublishGitHubEventResponse{AccountId: result.AccountID, Published: result.Published,
		Duplicate: result.Duplicate, Skipped: result.Skipped}, nil
}

func (b *githubdBridge) notifyEventPublished(ctx context.Context) {
	if b.notif != nil {
		_ = b.notif.Notify(ctx, db.NotifyEventPublished, "1")
	}
}
