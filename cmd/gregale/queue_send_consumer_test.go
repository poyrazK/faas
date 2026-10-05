package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeQueueConsumerClient struct {
	bindings    []api.QueueBindingResponse
	triggers    []api.Trigger
	bindingsErr error
}

func (f *fakeQueueConsumerClient) ListQueueBindings(context.Context, string) ([]api.QueueBindingResponse, error) {
	return f.bindings, f.bindingsErr
}

func (f *fakeQueueConsumerClient) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	return api.AppResponse{ID: "app-" + slug}, nil
}

func (f *fakeQueueConsumerClient) GetTriggers(context.Context, string, api.TriggerKind) ([]api.Trigger, error) {
	return f.triggers, nil
}

// TestUnconsumedQueueWarning — on production-us, `queue send --queue-name`
// to a name with no binding and no consumer reported success. The messages
// were never delivered and counted toward the app's queue depth limit.
func TestUnconsumedQueueWarning(t *testing.T) {
	cases := []struct {
		name   string
		queue  string
		client *fakeQueueConsumerClient
		warn   bool
	}{
		{"default queue", "", &fakeQueueConsumerClient{}, false},
		{"nothing reads it", "h3q", &fakeQueueConsumerClient{}, true},
		{"binding reads it", "h3q", &fakeQueueConsumerClient{bindings: []api.QueueBindingResponse{{QueueName: "h3q"}}}, false},
		{"other binding only", "h3q", &fakeQueueConsumerClient{bindings: []api.QueueBindingResponse{{QueueName: "orders"}}}, true},
		{"queue trigger reads it", "h3q", &fakeQueueConsumerClient{triggers: []api.Trigger{{Kind: api.TriggerKindQueue, Slug: "h3q", Enabled: true}}}, false},
		{"paused trigger", "h3q", &fakeQueueConsumerClient{triggers: []api.Trigger{{Kind: api.TriggerKindQueue, Slug: "h3q"}}}, true},
		{"lookup failed", "h3q", &fakeQueueConsumerClient{bindingsErr: errors.New("boom")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unconsumedQueueWarning(context.Background(), tc.client, "h3-rollout", tc.queue)
			if (got != "") != tc.warn {
				t.Fatalf("warning = %q, want warn=%v", got, tc.warn)
			}
			if tc.warn && !strings.Contains(got, "gregale queue bindings create h3-rollout --name h3q --queue-name h3q") {
				t.Fatalf("warning %q does not name the fix", got)
			}
		})
	}
}
