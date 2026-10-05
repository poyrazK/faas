package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

type queueConsumerClient interface {
	ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error)
	GetApp(context.Context, string) (api.AppResponse, error)
	GetTriggers(context.Context, string, api.TriggerKind) ([]api.Trigger, error)
}

// unconsumedQueueWarning returns a warning when nothing reads the named
// queue. If the app has no queue binding and no queue consumer, apid accepts
// a send to any queue name, and the generic drain skips named rows. On
// production-us, three messages sent to an unbound name sat in the queue,
// counted toward the app's depth cap, and `queue send` still reported
// success. An empty result means a consumer exists, the queue is the
// unnamed default, or the lookup failed (the send itself decides).
func unconsumedQueueWarning(ctx context.Context, client queueConsumerClient, slug, queueName string) string {
	if queueName == "" {
		return ""
	}
	bindings, err := client.ListQueueBindings(ctx, slug)
	if err != nil {
		return ""
	}
	for _, binding := range bindings {
		if binding.QueueName == queueName {
			return ""
		}
	}
	app, err := client.GetApp(ctx, slug)
	if err != nil || app.ID == "" {
		return ""
	}
	triggers, err := client.GetTriggers(ctx, app.ID, api.TriggerKindQueue)
	if err != nil {
		return ""
	}
	for _, trigger := range triggers {
		if trigger.Enabled && trigger.Slug == queueName {
			return ""
		}
	}
	return fmt.Sprintf("no queue binding or consumer reads queue %q on %s. The message waits, and counts toward the app's queue depth limit, until one exists: gregale queue bindings create %s --name %s --queue-name %s",
		queueName, slug, slug, queueName, queueName)
}
