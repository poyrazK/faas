package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

func pruneTimedGrants[K comparable](grants map[K]time.Time, now time.Time) {
	for key, until := range grants {
		if !until.After(now) {
			delete(grants, key)
		}
	}
}

func timedGrantCounts[K comparable](grants map[K]time.Time, is4 func(K) bool) map[bool]int {
	counts := map[bool]int{}
	for key := range grants {
		counts[is4(key)]++
	}
	return counts
}

func timedGrantCapacity[K comparable](current, additions map[K]time.Time, limit int, is4 func(K) bool) error {
	counts := timedGrantCounts(current, is4)
	for key := range additions {
		if _, exists := current[key]; !exists {
			counts[is4(key)]++
		}
	}
	if counts[true] > limit || counts[false] > limit {
		return fmt.Errorf("fcvm: timed egress capacity exceeded: per-family limit %d, IPv4 %d, IPv6 %d", limit, counts[true], counts[false])
	}
	return nil
}

func limitTimedSeeds[K comparable](current, seeds map[K]time.Time, limit int, is4 func(K) bool) {
	counts := timedGrantCounts(current, is4)
	for key := range seeds {
		if _, exists := current[key]; exists {
			continue
		}
		family := is4(key)
		if counts[family] >= limit {
			delete(seeds, key)
		} else {
			counts[family]++
		}
	}
}

// refreshTimedEgress runs under the instance's policy lock. The first add
// ensures deletion is valid for new keys; delete/add renews existing keys on
// Linux 6.8. If expiration races the delete (or an acknowledgement is lost),
// reconcile the affected sets from the still-valid committed cache plus this
// request. No other timed set or port/allowlist policy is changed.
func (m *Manager) refreshTimedEgress(ctx context.Context, id string, nc netns.Config, resolved map[netip.Addr]time.Time, pairs map[databaseEgressKey]time.Time) error {
	commands := timedEgressCommands(nc, resolved, pairs, true)
	if len(commands) == 0 {
		return nil
	}
	if err := m.runAtomicNetworkPolicy(ctx, nc.Netns, commands); err != nil {
		m.mu.Lock()
		inst := m.live[id]
		allResolved := map[netip.Addr]time.Time{}
		allPairs := map[databaseEgressKey]time.Time{}
		if inst != nil {
			if len(resolved) > 0 {
				for key, until := range inst.resolvedEgress {
					allResolved[key] = until
				}
			}
			if len(pairs) > 0 {
				for key, until := range inst.databaseEgress {
					allPairs[key] = until
				}
			}
		}
		m.mu.Unlock()
		for key, until := range resolved {
			allResolved[key] = until
		}
		for key, until := range pairs {
			allPairs[key] = until
		}
		var replacement [][]string
		for _, family := range []string{"ip", "ip6"} {
			if len(resolved) > 0 {
				replacement = append(replacement, []string{"ip", "netns", "exec", nc.Netns, "nft", "flush", "set", family, "faas", netns.EgressResolvedSet})
			}
			if len(pairs) > 0 {
				replacement = append(replacement, []string{"ip", "netns", "exec", nc.Netns, "nft", "flush", "set", family, "faas", netns.EgressDatabaseSet})
			}
		}
		replacement = append(replacement, timedEgressCommands(nc, allResolved, allPairs, false)...)
		if retryErr := m.runAtomicNetworkPolicy(ctx, nc.Netns, replacement); retryErr != nil {
			return errors.Join(err, fmt.Errorf("reconcile timed egress: %w", retryErr))
		}
	}
	return nil
}

func timedEgressCommands(nc netns.Config, resolved map[netip.Addr]time.Time, pairs map[databaseEgressKey]time.Time, refresh bool) [][]string {
	var commands [][]string
	now := time.Now()
	for address, until := range resolved {
		remaining := until.Sub(now)
		if remaining < time.Second {
			delete(resolved, address)
			continue
		}
		if refresh {
			commands = append(commands, nc.ResolvedEgressRefreshCommands([]netip.Addr{address}, remaining)...)
		} else {
			commands = append(commands, nc.ResolvedEgressAddCommands([]netip.Addr{address}, remaining)...)
		}
	}
	for pair, until := range pairs {
		remaining := until.Sub(now)
		if remaining < time.Second {
			delete(pairs, pair)
			continue
		}
		if refresh {
			commands = append(commands, nc.DatabaseEgressRefreshCommands([]netip.Addr{pair.addr}, pair.port, remaining)...)
		} else {
			commands = append(commands, nc.DatabaseEgressAddCommands([]netip.Addr{pair.addr}, pair.port, remaining)...)
		}
	}
	return commands
}
