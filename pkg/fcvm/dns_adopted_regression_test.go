// adr: 373 — DNS-gated egress; 20261104160000 — grant owner fences.
package fcvm

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A restarted vmmd adopts its predecessor's guests. Their namespaces still
// drop new connections to unresolved addresses, but the adoption record does
// not carry the DNS gate: an adopted Instance that looked ungated skipped
// every grant without an nft call or an error. After the rc.324 release and
// the rc.322 rollback on fsn-2, adopted VMs' egress_resolved sets stayed empty
// while fresh VMs' sets filled. Adoption reads the gate from the surviving
// namespace (restoreAdoptedEgress); the grant must then reach it end to end.
func TestAdoptedInstanceReceivesDNSGrants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		gated bool
	}{{"gating on", true}, {"gating off", false}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAdoptionFixture(t)
			m, vmm := adoptionManager(t, f, f.block())
			m.WithDNSGatedEgress(tc.gated)
			// The surviving namespace carries the gate exactly when the node did.
			m.WithCaptureRunner(&adoptedPolicyCapture{byFamily: map[string][]byte{
				"ip":  adoptedPolicyJSON("ip", api.TenantEgressBasePorts(), nil, nil, tc.gated),
				"ip6": adoptedPolicyJSON("ip6", api.TenantEgressBasePorts(), nil, nil, tc.gated),
			}})
			rep, err := m.adoptClassified(t.Context(), vmm, f.classifier())
			if err != nil || rep.Adopted != 1 {
				t.Fatalf("adopt = %+v, %v", rep, err)
			}
			owner, matched := m.ResolveServiceCaller(f.lease.HostIP)
			if !matched || owner.InstanceID != idLive || owner.AppID != "app-1" {
				t.Fatalf("adopted owner = %+v, %v", owner, matched)
			}
			run := m.run.(*fakeRunner)
			run.mu.Lock()
			before := len(run.commands)
			run.mu.Unlock()
			answer := []netip.Addr{netip.MustParseAddr("93.184.215.14")}
			if err := m.AllowResolvedEgress(t.Context(), f.lease.HostIP, owner, answer, 5*time.Minute, 5432); err != nil {
				t.Fatalf("AllowResolvedEgress: %v", err)
			}
			run.mu.Lock()
			added := strings.Join(flattenCommands(run.commands[before:]), "\n")
			run.mu.Unlock()
			resolved := strings.Contains(added, "egress_resolved") && strings.Contains(added, "93.184.215.14")
			database := strings.Contains(added, "egress_db")
			if resolved != tc.gated || !database {
				t.Fatalf("adopted guest grants: resolved=%v (want %v) database=%v:\n%s", resolved, tc.gated, database, added)
			}
		})
	}
}

func flattenCommands(cmds [][]string) []string {
	out := make([]string, 0, len(cmds))
	for _, argv := range cmds {
		out = append(out, strings.Join(argv, " "))
	}
	return out
}
