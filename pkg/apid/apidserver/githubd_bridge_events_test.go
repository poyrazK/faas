// adr: 20261027090010
package apidserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type bridgeNotifications struct{ channels []string }

func (n *bridgeNotifications) Notify(_ context.Context, channel, _ string) error {
	n.channels = append(n.channels, channel)
	return nil
}

func bridgeGitHubEvent(t *testing.T, title string) []byte {
	t.Helper()
	data, err := json.Marshal(api.GitHubPullRequestEvent{Action: "opened", InstallationID: 7,
		Repository: api.GitHubEventRepository{FullName: "octo/api"},
		PullRequest: api.GitHubEventPullRequest{Number: 42, Title: title, State: "open",
			Head: api.GitHubEventRef{Ref: "fix", SHA: strings.Repeat("a", 40), Repository: "octo/api"},
			Base: api.GitHubEventRef{Ref: "main", SHA: strings.Repeat("b", 40)}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGithubdBridgePublishGitHubEvent(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "triage@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: acct.ID, InstallationID: 7, AuditGithubLogin: "octocat"}); err != nil {
		t.Fatal(err)
	}
	notif := &bridgeNotifications{}
	inRollout := true
	bridge := apidserver.NewGithubdBridgeForTest(store, notif, func(a state.Account) bool { return inRollout && a.ID == acct.ID })
	request := func(delivery string, installation int64, title string) *githubdpb.PublishGitHubEventRequest {
		return &githubdpb.PublishGitHubEventRequest{DeliveryId: delivery, InstallationId: installation,
			EventType: "pull_request.opened", Data: bridgeGitHubEvent(t, title)}
	}
	const delivery = "72d3162e-cc78-11e3-81ab-4c9367dc0958"

	resp, err := bridge.PublishGitHubEvent(ctx, request(delivery, 7, "Fix"))
	if err != nil || !resp.GetPublished() || resp.GetDuplicate() || resp.GetAccountId() != acct.ID {
		t.Fatalf("PublishGitHubEvent() = %+v, %v", resp, err)
	}
	if len(notif.channels) != 1 || notif.channels[0] != db.NotifyEventPublished {
		t.Fatalf("notifications = %v", notif.channels)
	}
	resp, err = bridge.PublishGitHubEvent(ctx, request(delivery, 7, "Fix"))
	if err != nil || !resp.GetDuplicate() || len(notif.channels) != 1 {
		t.Fatalf("repeat = %+v, %v; notifications %v", resp, err, notif.channels)
	}
	if _, err := bridge.PublishGitHubEvent(ctx, request(delivery, 7, "Other")); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("conflicting delivery = %v, want AlreadyExists", err)
	}
	if _, err := bridge.PublishGitHubEvent(ctx, request("not-a-guid", 7, "Fix")); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed delivery = %v, want InvalidArgument", err)
	}
	other := "0f8fad5b-d9cb-469f-a165-70867728950e"
	resp, err = bridge.PublishGitHubEvent(ctx, &githubdpb.PublishGitHubEventRequest{DeliveryId: other, InstallationId: 8,
		EventType: "pull_request.opened", Data: []byte(strings.Replace(string(bridgeGitHubEvent(t, "Fix")), `"installation_id":7`, `"installation_id":8`, 1))})
	if err != nil || resp.GetPublished() || resp.GetSkipped() != "no_account" {
		t.Fatalf("unknown installation = %+v, %v", resp, err)
	}
	inRollout = false
	resp, err = bridge.PublishGitHubEvent(ctx, request(other, 7, "Fix"))
	if err != nil || resp.GetPublished() || resp.GetSkipped() != "not_enabled" || len(notif.channels) != 1 {
		t.Fatalf("outside rollout = %+v, %v", resp, err)
	}
}
