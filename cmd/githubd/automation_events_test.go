// adr: 20261027090010
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/githubd"
)

type publishGitHubEventClient struct {
	githubdpb.GithubdClient
	got *githubdpb.PublishGitHubEventRequest
	err error
}

func (c *publishGitHubEventClient) PublishGitHubEvent(_ context.Context, in *githubdpb.PublishGitHubEventRequest, _ ...grpc.CallOption) (*githubdpb.PublishGitHubEventResponse, error) {
	c.got = in
	if c.err != nil {
		return nil, c.err
	}
	return &githubdpb.PublishGitHubEventResponse{Published: true}, nil
}

func TestNewApidAutomationEvents(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	on := func(key string) string {
		if key == automationEventsEnv {
			return "1"
		}
		return ""
	}
	live := &liveApidBridgeClient{c: &publishGitHubEventClient{}}
	if newApidAutomationEvents(func(string) string { return "" }, live, log) != nil {
		t.Fatal("forwarding without the switch")
	}
	if newApidAutomationEvents(on, stubApidBridgeClient{}, log) != nil {
		t.Fatal("forwarding without a live bridge")
	}
	if newApidAutomationEvents(on, live, log) == nil {
		t.Fatal("not forwarding with the switch and a live bridge")
	}
}

func TestApidAutomationEventsMapsBridgeErrors(t *testing.T) {
	event := githubd.AutomationEvent{DeliveryID: "72d3162e-cc78-11e3-81ab-4c9367dc0958", InstallationID: 7,
		Type: "pull_request.opened", Data: []byte(`{}`)}
	cases := []struct {
		name     string
		err      error
		rejected bool
		ok       bool
	}{
		{"published", nil, false, true},
		{"invalid", status.Error(codes.InvalidArgument, "bad"), true, false},
		{"conflict", status.Error(codes.AlreadyExists, "other content"), true, false},
		{"older apid", status.Error(codes.Unimplemented, "unknown method"), true, false},
		{"unavailable", status.Error(codes.Unavailable, "down"), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &publishGitHubEventClient{err: tc.err}
			err := apidAutomationEvents{c: client}.PublishAutomationEvent(context.Background(), event)
			if tc.ok != (err == nil) || tc.rejected != errors.Is(err, githubd.ErrAutomationEventRejected) {
				t.Fatalf("PublishAutomationEvent() = %v", err)
			}
			if got := client.got; got.GetDeliveryId() != event.DeliveryID || got.GetInstallationId() != 7 ||
				got.GetEventType() != event.Type || string(got.GetData()) != "{}" {
				t.Fatalf("request = %+v", got)
			}
		})
	}
}
