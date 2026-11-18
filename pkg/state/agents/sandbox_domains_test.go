// adr: 20261117091026 §4 — a session's domain log keeps each grant and each
// refusal once, only under the session's lease and while it has a VM, and
// within the plan's grant budget and the refusal cap.
package agents_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxDomains(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{NetworkMode: "web"})
	if _, _, err := s.ClaimSandbox(ctx, "agentd", "tok-a", ttl, maxLive(5)); err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1_800_000_000_000)
	entry := func(domain, refused string, ms int) agents.SandboxDomain {
		return agents.SandboxDomain{Domain: domain, Refused: refused, FirstSeen: at.Add(time.Duration(ms) * time.Millisecond)}
	}
	if n, err := s.RecordSandboxDomains(ctx, sb.ID, "tok-a", api.PlanHobby, []agents.SandboxDomain{entry("example.com", "", 0)}); err != nil || n != 0 {
		t.Fatalf("before ready: %d, %v; want nothing kept", n, err)
	}
	if err := s.MarkSandboxReady(ctx, sb.ID, "tok-a", "node-a", "sandbox-vm"); err != nil {
		t.Fatal(err)
	}
	n, err := s.RecordSandboxDomains(ctx, sb.ID, "tok-a", api.PlanHobby, []agents.SandboxDomain{
		entry("example.com", "", 0), entry("admin.example.com", "policy", 1), entry("example.com", "", 2),
		entry("bad", "weird", 3), entry("", "", 4)})
	if err != nil || n != 2 {
		t.Fatalf("record = %d, %v; want 2", n, err)
	}
	if n, err = s.RecordSandboxDomains(ctx, sb.ID, "tok-b", api.PlanHobby, []agents.SandboxDomain{entry("other.example", "", 5)}); err != nil || n != 0 {
		t.Fatalf("another lease: %d, %v; want nothing kept", n, err)
	}
	if n, err = s.RecordSandboxDomains(ctx, sb.ID, "tok-a", api.PlanHobby, []agents.SandboxDomain{entry("example.com", "", 6)}); err != nil || n != 0 {
		t.Fatalf("a repeat: %d, %v; want nothing kept", n, err)
	}
	log, err := s.SandboxDomains(ctx, account, sb.ID)
	if err != nil || len(log) != 2 || log[0].Domain != "example.com" || !log[0].FirstSeen.Equal(at) ||
		log[1].Domain != "admin.example.com" || log[1].Refused != "policy" {
		t.Fatalf("log = %+v, %v", log, err)
	}
	if other, err := s.SandboxDomains(ctx, "00000000-0000-4000-8000-00000000beef", sb.ID); err != nil || len(other) != 0 {
		t.Fatalf("another account's read = %+v, %v", other, err)
	}

	// The plan's grant budget and the refusal cap bound the log.
	var many []agents.SandboxDomain
	grants := api.SandboxWebDistinctDomains[api.PlanHobby.PlanIndex()]
	for i := range grants + 5 {
		many = append(many, entry(fmt.Sprintf("g%d.example", i), "", i))
	}
	for i := range api.SandboxEgressRefusedLogMax + 5 {
		many = append(many, entry(fmt.Sprintf("r%d.example", i), "budget", i))
	}
	if n, err = s.RecordSandboxDomains(ctx, sb.ID, "tok-a", api.PlanHobby, many); err != nil {
		t.Fatal(err)
	}
	if want := grants - 1 + api.SandboxEgressRefusedLogMax - 1; n != want {
		t.Fatalf("kept %d; want %d", n, want)
	}
}
