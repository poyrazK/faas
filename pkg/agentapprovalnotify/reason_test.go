// adr: 20261110182236
package agentapprovalnotify_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The agent's reason reaches the email and the channel message, quoted as
// the agent's own words.
func TestNotifier_ShowsTheAgentsReason(t *testing.T) {
	f := newFixture(t)
	channels := &fakeChannels{}
	f.notifier.Channels = channels
	ctx := t.Context()
	connector := f.account("reason@example.com")
	ch, err := f.store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: connector.ID, Name: "ops", Kind: "email", Email: "ops@example.com"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetAgentApprovalChannels(ctx, connector.ID, "", []string{ch.ID}); err != nil {
		t.Fatal(err)
	}
	key := f.key(connector, "reasoning-agent")
	const id = "00000000-0000-4000-8000-000000000ccc"
	if _, err := f.store.RequestAgentApproval(ctx, state.AgentApproval{
		ID: id, KeyID: key.ID, AccountID: key.AccountID, Category: api.AgentApprovalDelete,
		Method: "DELETE", Path: "/v1/apps/ccc", ExpiresAt: time.Now().Add(api.AgentApprovalTTL),
		ReviewURL: "https://gregale.test/cli-auth?agent_approval=" + id, AgentName: key.Label,
		Reason: `drop the "old" preview app`,
	}); err != nil {
		t.Fatal(err)
	}

	f.run()
	const quoted = `The agent's reason (its own words): "drop the \"old\" preview app"`
	sent := f.mail.take()
	if len(sent) != 1 || !strings.Contains(sent[0].body, quoted) {
		t.Fatalf("email = %+v; want the quoted reason", sent)
	}
	if len(channels.sent) != 1 || !strings.Contains(channels.sent[0].Summary, quoted) {
		t.Fatalf("channel messages = %+v; want the quoted reason", channels.sent)
	}
}
