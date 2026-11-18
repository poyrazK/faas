// adr: 20261117091026 §2–§3 — an account's default policy reads empty until
// set and is replaced whole; a session keeps the policy it was admitted
// under, which its claim carries to agentd.
package agents_test

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxEgressPolicies(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	p, err := s.SandboxEgressPolicy(ctx, account)
	if err != nil || p.Allow == nil || len(p.Allow)+len(p.Deny) != 0 {
		t.Fatalf("an unset policy = %+v, %v", p, err)
	}
	if _, err := s.PutSandboxEgressPolicy(ctx, account, []string{"example.com"}, []string{"evil.example.com"}); err != nil {
		t.Fatal(err)
	}
	if p, err = s.PutSandboxEgressPolicy(ctx, account, nil, []string{"other.example"}); err != nil || len(p.Allow) != 0 ||
		!slices.Equal(p.Deny, []string{"other.example"}) {
		t.Fatalf("a replaced policy = %+v, %v", p, err)
	}
	if p, err = s.SandboxEgressPolicy(ctx, account); err != nil || len(p.Allow) != 0 || !slices.Equal(p.Deny, []string{"other.example"}) {
		t.Fatalf("read back = %+v, %v", p, err)
	}

	sb := admitSandbox(t, s, agents.SandboxAdmission{NetworkMode: "web", EgressAllow: []string{"example.com"},
		EgressDeny: []string{"admin.example.com"}})
	plain := admitSandbox(t, s, agents.SandboxAdmission{NetworkMode: "web"})
	got, err := s.Sandbox(ctx, account, sb.ID)
	if err != nil || !slices.Equal(got.EgressAllow, []string{"example.com"}) || !slices.Equal(got.EgressDeny, []string{"admin.example.com"}) {
		t.Fatalf("session = %+v, %v", got, err)
	}
	if got, err := s.Sandbox(ctx, account, plain.ID); err != nil || len(got.EgressAllow)+len(got.EgressDeny) != 0 {
		t.Fatalf("a session without a policy = %+v, %v", got, err)
	}
	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID || !slices.Equal(claim.EgressAllow, []string{"example.com"}) ||
		!slices.Equal(claim.EgressDeny, []string{"admin.example.com"}) {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
}
