// adr: 20261107140000
package agentapprovalnotify_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentapprovalnotify"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type sentMail struct{ to, subject, body, key string }

type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (f *fakeMailer) SendEmail(_ context.Context, to, subject, body, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMail{to, subject, body, key})
	return nil
}

func (f *fakeMailer) take() []sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

type fixture struct {
	t        *testing.T
	store    *state.MemStore
	mail     *fakeMailer
	notifier *agentapprovalnotify.Notifier
	outcomes []string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, store: state.NewMemStore(), mail: &fakeMailer{}}
	f.notifier = &agentapprovalnotify.Notifier{Store: f.store, Mail: f.mail, Outcome: func(o string) { f.outcomes = append(f.outcomes, o) }}
	return f
}

func (f *fixture) account(email string) state.Account {
	f.t.Helper()
	acct, err := f.store.CreateAccount(f.t.Context(), email, api.PlanPro)
	if err != nil {
		f.t.Fatal(err)
	}
	return acct
}

func (f *fixture) key(acct state.Account, label string) state.APIKey {
	f.t.Helper()
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		f.t.Fatal(err)
	}
	key, err := f.store.CreateAPIKey(f.t.Context(), acct.ID, hash, label, []string{api.ScopeAgent, api.ScopeDeployWrite})
	if err != nil {
		f.t.Fatal(err)
	}
	return key
}

func (f *fixture) request(key state.APIKey, orgID, path string) state.AgentApproval {
	f.t.Helper()
	id := "00000000-0000-4000-8000-" + strings.Repeat("0", 12-len(path)) + path
	a, err := f.store.RequestAgentApproval(f.t.Context(), state.AgentApproval{
		ID: id, KeyID: key.ID, AccountID: key.AccountID, OrgID: orgID, Category: api.AgentApprovalDelete,
		Method: "DELETE", Path: "/v1/apps/" + path, ExpiresAt: time.Now().Add(api.AgentApprovalTTL),
		ReviewURL: "https://gregale.test/cli-auth?agent_approval=" + id, AgentName: key.Label,
	})
	if err != nil {
		f.t.Fatalf("RequestAgentApproval: %v", err)
	}
	return a
}

func (f *fixture) run() {
	f.t.Helper()
	f.outcomes = nil
	if err := f.notifier.RunOnce(f.t.Context()); err != nil {
		f.t.Fatalf("RunOnce: %v", err)
	}
}

// The connector is emailed the review link; a second request of the same
// key within the interval waits for the next email instead of sending one.
func TestNotifier_EmailsTheConnectorOncePerInterval(t *testing.T) {
	f := newFixture(t)
	connector := f.account("connector@example.com")
	key := f.key(connector, "ci-agent")
	first := f.request(key, "", "aaa")

	f.run()
	sent := f.mail.take()
	if len(sent) != 1 || sent[0].to != "connector@example.com" || !strings.Contains(sent[0].subject, "ci-agent") ||
		!strings.Contains(sent[0].body, "DELETE /v1/apps/aaa") || !strings.Contains(sent[0].body, "https://gregale.test/cli-auth?agent_approval="+first.ID) {
		t.Fatalf("first email = %+v", sent)
	}
	if strings.Contains(sent[0].body, "{") {
		t.Fatalf("an email must not carry a request body: %s", sent[0].body)
	}

	f.request(key, "", "bbb")
	f.run()
	if sent := f.mail.take(); len(sent) != 0 || len(f.outcomes) != 1 || f.outcomes[0] != agentapprovalnotify.OutcomeDeferred {
		t.Fatalf("second request within the interval = %+v, outcomes %v; want it deferred", sent, f.outcomes)
	}
	f.run()
	if len(f.outcomes) != 0 {
		t.Fatalf("a deferred notification came due early: %v", f.outcomes)
	}
}

// A request decided before the notifier runs sends nothing.
func TestNotifier_SkipsDecidedRequests(t *testing.T) {
	f := newFixture(t)
	connector := f.account("decider@example.com")
	key := f.key(connector, "fast-agent")
	a := f.request(key, "", "ccc")
	if err := f.store.DecideAgentApproval(t.Context(), a.ID, true, connector.ID, ""); err != nil {
		t.Fatal(err)
	}
	f.run()
	if sent := f.mail.take(); len(sent) != 0 || len(f.outcomes) != 1 || f.outcomes[0] != agentapprovalnotify.OutcomeSkipped {
		t.Fatalf("decided request = %+v, outcomes %v; want skipped", sent, f.outcomes)
	}
}

// When the connector has left the organization the key acts in, its owners
// and admins are emailed instead; plain members are not.
func TestNotifier_FallsBackToOrgAdmins(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	owner := f.account("owner@example.com")
	admin := f.account("admin@example.com")
	member := f.account("member@example.com")
	departed := f.account("departed@example.com")
	org, err := f.store.CreateOrg(ctx, state.Org{Slug: "acme-agents", Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		acct state.Account
		role state.OrgRole
	}{{owner, state.OrgRoleOwner}, {admin, state.OrgRoleAdmin}, {member, state.OrgRoleDeveloper}, {departed, state.OrgRoleAdmin}} {
		if err := f.store.AddOrgMember(ctx, org.ID, m.acct.ID, m.role, nil); err != nil {
			t.Fatalf("AddOrgMember: %v", err)
		}
	}
	if err := f.store.RemoveOrgMember(ctx, org.ID, departed.ID); err != nil {
		t.Fatal(err)
	}
	key := f.key(departed, "org-agent")
	f.request(key, org.ID, "ddd")

	f.run()
	got := map[string]bool{}
	for _, m := range f.mail.take() {
		got[m.to] = true
	}
	if len(got) != 2 || !got["owner@example.com"] || !got["admin@example.com"] {
		t.Fatalf("emailed %v; want the org's owner and admin only", got)
	}
}

// A key acting in its owner's personal organization emails the owner, who
// may have no membership row there.
func TestNotifier_PersonalOrganizationOwnerIsTheRecipient(t *testing.T) {
	f := newFixture(t)
	owner := f.account("solo@example.com")
	key := f.key(owner, "solo-agent")
	personal, err := f.store.CreateOrg(t.Context(), state.Org{Slug: "solo-personal", Name: "Solo", Personal: true, PersonalOwnerAccountID: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.request(key, personal.ID, "eee")
	f.run()
	if sent := f.mail.take(); len(sent) != 1 || sent[0].to != "solo@example.com" {
		t.Fatalf("emailed %+v; want the personal organization's owner", sent)
	}
}
