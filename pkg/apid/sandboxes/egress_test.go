// adr: 20261117091026 §1–§2 — an account's default egress policy is read
// and replaced only as domain rules and only with the switch on; a web-mode
// session takes the account's deny plus its own and an allow that narrows
// the account's, never widens it; other modes take no policy.
package sandboxes_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func (f *fakeStore) SandboxEgressPolicy(context.Context, string) (agentstore.SandboxEgressPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.egress, nil
}

func (f *fakeStore) PutSandboxEgressPolicy(_ context.Context, _ string, allow, deny []string) (agentstore.SandboxEgressPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.egress = agentstore.SandboxEgressPolicy{Allow: allow, Deny: deny}
	return f.egress, nil
}

func (f *fakeStore) SandboxDomains(_ context.Context, accountID, id string) ([]agentstore.SandboxDomain, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.find(accountID, id); err != nil {
		return nil, err
	}
	return f.domains[id], nil
}

// adr: 20261117091026 §4 — a session's egress record shows the policy it
// was admitted under and its domain log, only to its account.
func TestSessionEgress(t *testing.T) {
	f := newFixture(t)
	f.h.WebNetwork, f.h.EgressPolicy = true, true
	var sb api.Sandbox
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes",
		`{"preset":"codex","network":{"mode":"web","allow":["example.com"]}}`, "", &sb); code >= 300 {
		t.Fatalf("create = %d", code)
	}
	at := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	f.store.domains = map[string][]agentstore.SandboxDomain{sb.ID: {{Domain: "example.com", FirstSeen: at},
		{Domain: "evil.example", Refused: "policy", FirstSeen: at.Add(time.Second)}}}
	var got api.SandboxEgress
	if code := f.call(t, f.h.GetEgress, pro(), http.MethodGet, "/v1/sandboxes/"+sb.ID+"/egress", "", sb.ID, &got); code != http.StatusOK ||
		got.SandboxID != sb.ID || got.Mode != api.SandboxNetworkWeb || !slices.Equal(got.Allow, []string{"example.com"}) ||
		got.Deny == nil || len(got.Domains) != 2 || got.Domains[0] != (api.SandboxEgressDomain{Domain: "example.com", FirstSeen: at}) ||
		got.Domains[1].Refused != "policy" {
		t.Fatalf("egress = %d %+v", code, got)
	}
	plain, _ := f.create(t, pro(), `{"preset":"codex"}`)
	got = api.SandboxEgress{}
	if code := f.call(t, f.h.GetEgress, pro(), http.MethodGet, "", "", plain.ID, &got); code != http.StatusOK ||
		got.Allow == nil || got.Domains == nil || len(got.Domains) != 0 {
		t.Fatalf("a session without a log = %d %+v", code, got)
	}
	other := pro()
	other.ID = "33333333-3333-3333-3333-333333333333"
	if code := f.call(t, f.h.GetEgress, other, http.MethodGet, "", "", sb.ID, nil); code != http.StatusNotFound {
		t.Fatalf("another account's read = %d", code)
	}
}

func TestEgressPolicyRoutes(t *testing.T) {
	f := newFixture(t)
	var p api.SandboxEgressPolicy
	if code := f.call(t, f.h.GetEgressPolicy, pro(), http.MethodGet, "/v1/sandbox-egress-policy", "", "", &p); code != http.StatusOK ||
		p.Allow == nil || len(p.Allow)+len(p.Deny) != 0 {
		t.Fatalf("an empty policy = %d %+v", code, p)
	}
	body := `{"allow":["example.com","docs.example.com","example.com"],"deny":["admin.example.com"]}`
	var prob api.Problem
	if code := f.call(t, f.h.PutEgressPolicy, pro(), http.MethodPut, "/v1/sandbox-egress-policy", body, "", &prob); prob.Code != api.CodeSandboxUnsupported {
		t.Fatalf("with the switch off = %d %+v", code, prob)
	}
	f.h.EgressPolicy = true
	if code := f.call(t, f.h.PutEgressPolicy, pro(), http.MethodPut, "/v1/sandbox-egress-policy", body, "", &p); code != http.StatusOK ||
		!slices.Equal(p.Allow, []string{"docs.example.com", "example.com"}) || !slices.Equal(p.Deny, []string{"admin.example.com"}) {
		t.Fatalf("put = %d %+v", code, p)
	}
	for _, bad := range []string{`{"allow":["*.example.com"]}`, `{"deny":["localhost"]}`, `{"allow":["Example.com"]}`} {
		if code := f.call(t, f.h.PutEgressPolicy, pro(), http.MethodPut, "/v1/sandbox-egress-policy", bad, "", nil); code < 400 || code >= 500 {
			t.Errorf("%s = %d", bad, code)
		}
	}
}

func TestSessionEgressPolicy(t *testing.T) {
	f := newFixture(t)
	f.h.WebNetwork = true
	create := func(body string) (int, api.Problem) {
		var p api.Problem
		code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", body, "", &p)
		return code, p
	}
	if code, p := create(`{"preset":"codex","network":{"mode":"web","allow":["example.com"]}}`); p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("a policy with the switch off = %d %+v", code, p)
	}
	if code, _ := create(`{"preset":"codex","network":{"mode":"web"}}`); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("a web session without a policy with the switch off = %d", code)
	}
	f.h.EgressPolicy = true
	f.store.egress = agentstore.SandboxEgressPolicy{Allow: []string{"example.com"}, Deny: []string{"evil.example.com"}}
	for _, tc := range []struct {
		body        string
		allow, deny []string
		refused     bool
	}{
		{body: `{"preset":"codex","network":{"mode":"web"}}`, allow: []string{"example.com"}, deny: []string{"evil.example.com"}},
		{body: `{"preset":"codex","network":{"mode":"web","allow":["docs.example.com"],"deny":["old.docs.example.com"]}}`,
			allow: []string{"docs.example.com"}, deny: []string{"evil.example.com", "old.docs.example.com"}},
		{body: `{"preset":"codex","network":{"mode":"web","allow":["example.org"]}}`, refused: true},
		{body: `{"preset":"codex","network":{"mode":"registries","deny":["example.com"]}}`, refused: true},
		{body: `{"preset":"codex","network":{"mode":"web","deny":["10.0.0.1"]}}`, refused: true},
	} {
		code, p := create(tc.body)
		if tc.refused {
			if code < 400 || code >= 500 {
				t.Errorf("%s = %d %+v; want a refusal", tc.body, code, p)
			}
			continue
		}
		a := f.store.admitted
		if code >= 300 || !slices.Equal(a.EgressAllow, tc.allow) || !slices.Equal(a.EgressDeny, tc.deny) {
			t.Errorf("%s = %d, admitted allow %v deny %v", tc.body, code, a.EgressAllow, a.EgressDeny)
		}
	}
}
