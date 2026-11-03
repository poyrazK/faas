package main

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/githubd"
)

// automationEventsEnv turns on forwarding pull_request deliveries to apid as
// gregale.github events (ADR-20261027090010 §9). apid publishes them only for accounts
// the agents capability is released to.
const automationEventsEnv = "FAAS_GITHUBD_AUTOMATION_EVENTS"

// apidAutomationEvents forwards deliveries over the apid bridge.
type apidAutomationEvents struct{ c githubdpb.GithubdClient }

func (a apidAutomationEvents) PublishAutomationEvent(ctx context.Context, e githubd.AutomationEvent) error {
	_, err := a.c.PublishGitHubEvent(ctx, &githubdpb.PublishGitHubEventRequest{DeliveryId: e.DeliveryID,
		InstallationId: e.InstallationID, EventType: e.Type, Data: e.Data})
	switch status.Code(err) {
	case codes.OK:
		return nil
	// An apid that predates the RPC cannot publish the event however often
	// the delivery is retried.
	case codes.InvalidArgument, codes.AlreadyExists, codes.Unimplemented:
		return fmt.Errorf("%w: %w", githubd.ErrAutomationEventRejected, err)
	}
	return err
}

// newApidAutomationEvents returns the forwarder when the switch is on and
// the bridge is live, and nil otherwise.
func newApidAutomationEvents(getenv func(string) string, client ApidBridgeClient, log *slog.Logger) githubd.AutomationEventPublisher {
	if getenv(automationEventsEnv) != "1" {
		return nil
	}
	live, ok := client.(*liveApidBridgeClient)
	if !ok || live.c == nil {
		log.Warn("githubd: automation events need the apid bridge; not forwarding pull_request deliveries", "env", automationEventsEnv)
		return nil
	}
	log.Info("githubd: forwarding pull_request deliveries to automations")
	return apidAutomationEvents{c: live.c}
}
