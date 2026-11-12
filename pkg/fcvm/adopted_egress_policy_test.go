// adr: 20261105103000
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

type adoptedPolicyCapture struct {
	byFamily map[string][]byte
	err      error
}

func (c *adoptedPolicyCapture) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	if len(argv) != 11 || argv[5] != "-j" {
		return nil, nil // No public allowlist handle in these fixtures.
	}
	family := argv[9]
	if out, ok := c.byFamily[family]; ok {
		return out, nil
	}
	return adoptedPolicyJSON(family, api.TenantEgressBasePorts(), nil, nil, true), nil
}

func adoptedPolicyJSON(family string, ports []uint16, resolved, databases []json.RawMessage, gated bool) []byte {
	sets := []any{
		map[string]any{"set": map[string]any{"family": family, "table": "faas", "name": netns.EgressPortsSet, "elem": ports}},
		map[string]any{"set": map[string]any{"family": family, "table": "faas", "name": netns.EgressDatabaseSet, "elem": databases}},
	}
	if gated {
		sets = append(sets, map[string]any{"set": map[string]any{"family": family, "table": "faas", "name": netns.EgressResolvedSet, "elem": resolved}})
	}
	out, _ := json.Marshal(map[string]any{"nftables": sets})
	return out
}

func TestAdoptionRestoresLiveEgressAndOriginalExpiries(t *testing.T) {
	f := newAdoptionFixture(t)
	m, vmm := adoptionManager(t, f, f.block())
	ports := append(api.TenantEgressBasePorts(), 81, 5432)
	m.WithCaptureRunner(&adoptedPolicyCapture{byFamily: map[string][]byte{
		"ip": adoptedPolicyJSON("ip", ports,
			[]json.RawMessage{json.RawMessage(`{"elem":{"val":"203.0.113.10","timeout":600000,"expires":12000}}`), json.RawMessage(`{"elem":{"val":"203.0.113.11","expires":1}}`)},
			[]json.RawMessage{json.RawMessage(`{"elem":{"val":{"concat":["203.0.113.10",5432]},"expires":17000}}`)}, true),
		"ip6": adoptedPolicyJSON("ip6", ports,
			[]json.RawMessage{json.RawMessage(`{"elem":{"val":"2001:db8::10","expires":11000}}`)}, nil, true),
	}})
	run := &fakeInputRunner{}
	m.run = run
	before := time.Now()
	if report, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || report.Adopted != 1 {
		t.Fatalf("adoption=%+v err=%v", report, err)
	}
	inst := m.live[idLive]
	if !slices.Equal(inst.Net.EgressPorts, ports) || !inst.Net.DNSGated || !inst.Net.DatabaseEgress || inst.Net.EgressConnRate == 0 || inst.Net.EgressDestConnRate == 0 {
		t.Fatalf("incomplete adopted policy: %+v", inst.Net)
	}
	v4, v6 := netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::10")
	if len(inst.resolvedEgress) != 2 || len(inst.databaseEgress) != 1 {
		t.Fatal("lost live grants or retained a subsecond grant")
	}
	for addr, duration := range map[netip.Addr]time.Duration{v4: 12 * time.Second, v6: 11 * time.Second} {
		until := inst.resolvedEgress[addr]
		if until.Before(before.Add(duration)) || until.After(time.Now().Add(duration)) {
			t.Fatalf("grant %s renewed or shortened unexpectedly: %s", addr, until)
		}
	}
	dnsUntil, dbUntil := inst.resolvedEgress[v4], inst.databaseEgress[databaseEgressKey{v4, 5432}]
	// The journal's empty allowlist cannot prove the live policy; the first
	// ordinary projection must rebuild and retain these surviving permissions.
	if err := m.UpdateAppEgressPolicy(t.Context(), inst.AppID, 1, nil, []uint16{81, 5432}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"delete table ip faas", "ip daddr != @egress_resolved", "limit rate over", "203.0.113.10 timeout 11s", "203.0.113.10 . 5432 timeout 16s", "2001:db8::10 timeout 10s"} {
		if !strings.Contains(string(run.input), want) {
			t.Fatalf("rebuild lost original policy %q: %s", want, run.input)
		}
	}
	if inst.resolvedEgress[v4] != dnsUntil || inst.databaseEgress[databaseEgressKey{v4, 5432}] != dbUntil {
		t.Fatal("rebuild renewed restored permissions")
	}
}

func TestAdoptionRefusesMissingOrInconsistentEgress(t *testing.T) {
	for _, scenario := range []string{"capture-error", "missing-sets", "invalid-expiry", "different-ports", "missing-dns", "forbidden-port"} {
		t.Run(scenario, func(t *testing.T) {
			f := newAdoptionFixture(t)
			m, vmm := adoptionManager(t, f, f.block())
			capture := &adoptedPolicyCapture{byFamily: map[string][]byte{}}
			ports := api.TenantEgressBasePorts()
			switch scenario {
			case "capture-error":
				capture.err = errors.New("capture unavailable")
			case "missing-sets":
				capture.byFamily["ip"] = []byte(`{"nftables":[]}`)
			case "invalid-expiry":
				capture.byFamily["ip"] = adoptedPolicyJSON("ip", ports, []json.RawMessage{json.RawMessage(`{"elem":{"val":"203.0.113.10","timeout":600000}}`)}, nil, true)
			case "different-ports":
				capture.byFamily["ip6"] = adoptedPolicyJSON("ip6", append(ports, 5432), nil, nil, true)
			case "missing-dns":
				for _, family := range []string{"ip", "ip6"} {
					capture.byFamily[family] = adoptedPolicyJSON(family, ports, nil, nil, false)
				}
			case "forbidden-port":
				for _, family := range []string{"ip", "ip6"} {
					capture.byFamily[family] = adoptedPolicyJSON(family, append(ports, 22), nil, nil, true)
				}
			}
			m.WithCaptureRunner(capture)
			report, err := m.adoptClassified(t.Context(), vmm, f.classifier())
			if err != nil || report.Adopted != 0 || report.Refused[adoptionRefusedAdoptFailed] != 1 {
				t.Fatalf("unsafe adoption=%+v err=%v", report, err)
			}
			if m.LiveCount() != 0 || len(vmm.adopted) != 0 || !m.isRestartQuarantined(idLive) {
				t.Fatal("incomplete egress policy escaped quarantine")
			}
		})
	}
}

func TestAdoptionPreservesExplicitlyDisabledDNSGating(t *testing.T) {
	f := newAdoptionFixture(t)
	m, vmm := adoptionManager(t, f, f.block())
	m.dnsGatingOff = true
	m.WithCaptureRunner(&adoptedPolicyCapture{byFamily: map[string][]byte{
		"ip":  adoptedPolicyJSON("ip", api.TenantEgressBasePorts(), nil, nil, false),
		"ip6": adoptedPolicyJSON("ip6", api.TenantEgressBasePorts(), nil, nil, false),
	}})
	if report, err := m.adoptClassified(t.Context(), vmm, f.classifier()); err != nil || report.Adopted != 1 || m.live[idLive].Net.DNSGated {
		t.Fatalf("adoption=%+v err=%v", report, err)
	}
}

func TestAdoptedTimedEgressRejectsUnverifiableElements(t *testing.T) {
	for _, raw := range []string{
		`{"elem":{"val":"203.0.113.10"}}`,
		`{"elem":{"val":"203.0.113.10","expires":-1}}`,
		`{"elem":{"val":"203.0.113.10","expires":3600001}}`,
		`{"elem":{"val":"2001:db8::10","expires":1000}}`,
		`{"elem":{"val":"::ffff:203.0.113.10","expires":1000}}`,
		`{"elem":{"val":"invalid","expires":1000}}`,
	} {
		if _, _, err := parseAdoptedEgressElement(json.RawMessage(raw), false, "ip", time.Now()); err == nil {
			t.Fatalf("unverifiable grant accepted: %s", raw)
		}
	}
}
