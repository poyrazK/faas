package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

// The namespace is already anchored to the proven survivor. Read its applied
// port policy and remaining timed permissions before publishing the instance.
// Journal-era defaults cannot describe the effective policy after live updates.
func (m *Manager) restoreAdoptedEgress(ctx context.Context, inst *Instance) error {
	if inst.Lease.Networkless {
		return nil
	}
	capture, err := m.networkCaptureRunner(ctx)
	if err != nil {
		return err
	}
	if capture == nil {
		return errors.New("adoption requires the surviving egress policy")
	}
	var snapshots []adoptedEgressSnapshot
	for _, family := range []string{"ip", "ip6"} {
		// Count elapsed capture time against the remaining lifetime, never
		// renew a grant because a capture or a later rebuild was slow.
		observed := time.Now()
		out, err := capture.RunCapture(ctx, []string{"ip", "netns", "exec", inst.Net.Netns, "nft", "-j", "-n", "list", "table", family, "faas"})
		if err != nil {
			return fmt.Errorf("read adopted %s egress policy: %w", family, err)
		}
		snapshot, err := parseAdoptedEgress(out, family, observed)
		if err != nil {
			return fmt.Errorf("read adopted %s egress policy: %w", family, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	v4, v6 := snapshots[0], snapshots[1]
	if !slices.Equal(v4.ports, v6.ports) || v4.gated != v6.gated || !v4.gated && !m.dnsGatingOff {
		return errors.New("surviving egress policy is inconsistent or missing DNS gating")
	}
	// Reapply the current plan's connection limits. Preserve the exact live
	// ports only if they still satisfy that plan's canonical policy.
	applyTenantEgressPolicy(&inst.Net, inst.Plan, v4.ports)
	canonical := slices.Clone(inst.Net.EgressPorts)
	slices.Sort(canonical)
	if !slices.Equal(canonical, v4.ports) {
		return errors.New("surviving egress ports do not match the workload plan")
	}
	inst.Net.DNSGated = v4.gated
	inst.resolvedEgress = v4.resolved
	inst.databaseEgress = v4.databases
	for addr, until := range v6.resolved {
		inst.resolvedEgress[addr] = until
	}
	for pair, until := range v6.databases {
		inst.databaseEgress[pair] = until
	}
	// The allowlist came from the journal, which may precede an interrupted
	// live write. Its first projection must reconstruct even an empty list.
	inst.egressAllowlistUncertain = true
	return nil
}

type adoptedEgressSnapshot struct {
	ports     []uint16
	gated     bool
	resolved  map[netip.Addr]time.Time
	databases map[databaseEgressKey]time.Time
}

type adoptedEgressSet struct {
	Family string            `json:"family"`
	Table  string            `json:"table"`
	Name   string            `json:"name"`
	Elem   []json.RawMessage `json:"elem"`
}

func parseAdoptedEgress(out []byte, family string, observed time.Time) (adoptedEgressSnapshot, error) {
	snapshot := adoptedEgressSnapshot{resolved: map[netip.Addr]time.Time{}, databases: map[databaseEgressKey]time.Time{}}
	var doc struct {
		Nftables []struct {
			Set *adoptedEgressSet `json:"set"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return snapshot, fmt.Errorf("parse surviving nft sets: %w", err)
	}
	seen := map[string]bool{}
	for _, item := range doc.Nftables {
		set := item.Set
		if set == nil || set.Name != netns.EgressPortsSet && set.Name != netns.EgressResolvedSet && set.Name != netns.EgressDatabaseSet {
			continue
		}
		if set.Family != family || set.Table != "faas" || seen[set.Name] {
			return snapshot, errors.New("ambiguous surviving egress set")
		}
		seen[set.Name] = true
		if err := snapshot.readSet(*set, family, observed); err != nil {
			return snapshot, err
		}
	}
	if !seen[netns.EgressPortsSet] || !seen[netns.EgressDatabaseSet] {
		return snapshot, errors.New("surviving egress sets are missing")
	}
	snapshot.gated = seen[netns.EgressResolvedSet]
	slices.Sort(snapshot.ports)
	snapshot.ports = slices.Compact(snapshot.ports)
	return snapshot, nil
}

func (s *adoptedEgressSnapshot) readSet(set adoptedEgressSet, family string, observed time.Time) error {
	for _, raw := range set.Elem {
		if set.Name == netns.EgressPortsSet {
			var port uint16
			if err := json.Unmarshal(raw, &port); err != nil || port == 0 {
				return errors.New("invalid surviving egress port")
			}
			s.ports = append(s.ports, port)
			continue
		}
		pair, until, err := parseAdoptedEgressElement(raw, set.Name == netns.EgressDatabaseSet, family, observed)
		if err != nil {
			return err
		}
		if until.Sub(observed) < time.Second {
			continue
		}
		if set.Name == netns.EgressResolvedSet {
			s.resolved[pair.addr] = until
		} else {
			s.databases[pair] = until
		}
	}
	return nil
}

// nft JSON represents timed elements as {"elem":{"val":...,"expires":ms}}.
// A database value is {"concat":[address,port]}. Require the remaining expiry;
// the original timeout alone would renew an old grant across a restart.
func parseAdoptedEgressElement(raw json.RawMessage, database bool, family string, observed time.Time) (databaseEgressKey, time.Time, error) {
	var wrapped struct {
		Elem *struct {
			Val     json.RawMessage `json:"val"`
			Expires *uint64         `json:"expires"`
		} `json:"elem"`
	}
	invalid := errors.New("invalid surviving timed egress permission")
	var pair databaseEgressKey
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Elem == nil || wrapped.Elem.Expires == nil ||
		*wrapped.Elem.Expires > uint64(api.DNSGatedEgressMaxTTLSeconds)*1000 {
		return pair, time.Time{}, invalid
	}
	val := wrapped.Elem.Val
	if database {
		var tuple struct {
			Concat []json.RawMessage `json:"concat"`
		}
		if err := json.Unmarshal(val, &tuple); err != nil || len(tuple.Concat) != 2 {
			return pair, time.Time{}, invalid
		}
		val = tuple.Concat[0]
		if err := json.Unmarshal(tuple.Concat[1], &pair.port); err != nil || pair.port == 0 {
			return pair, time.Time{}, invalid
		}
	}
	if err := json.Unmarshal(val, &pair.addr); err != nil || !pair.addr.IsValid() || pair.addr.Zone() != "" ||
		pair.addr.Is4In6() || pair.addr.Is4() != (family == "ip") {
		return pair, time.Time{}, invalid
	}
	return pair, observed.Add(time.Duration(*wrapped.Elem.Expires) * time.Millisecond), nil
}
