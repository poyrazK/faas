// adr: 20261027163626 — hierarchical name locks for the traffic binding guard.
package state

import (
	"sort"
	"strings"
)

// A scoped binding write locks the hostnames it may change, not the fleet.
// An exact hostname takes its host key exclusively. A subtree, every strict
// descendant of a name (a wildcard domain, or a route pattern's
// match_host_scope), takes its tree key exclusively; the empty name is every
// hostname. Both take each proper ancestor's tree key shared. So two writes
// conflict only when they name the same host or subtree, or one's names lie
// inside the other's subtree. The root tree key is the previous release's
// global route lock, which it holds exclusively for every binding write.
const (
	trafficHostLockPrefix = "gregale.traffic.host.v1:"
	trafficTreeLockPrefix = "gregale.traffic.tree.v1:"
)

type trafficSessionLock struct {
	Key    string
	Shared bool
}

func trafficTreeLockKey(name string) string {
	if name == "" {
		return globalTrafficRoutesLock
	}
	return trafficTreeLockPrefix + name
}

func normalizeTrafficLockName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// trafficNameLocks returns the session locks for exact hostnames and subtree
// roots, root first, then shallower names first. An empty host is ignored.
func trafficNameLocks(hosts, subtrees []string) []trafficSessionLock {
	shared := map[string]bool{globalTrafficRoutesLock: true}
	depth := map[string]int{globalTrafficRoutesLock: 0}
	want := func(key string, labels int, exclusive bool) {
		if current, seen := shared[key]; !seen || current {
			shared[key] = !exclusive
		}
		depth[key] = labels
	}
	ancestors := func(name string) {
		for labels, tail := strings.Count(name, ".")+1, name; strings.ContainsRune(tail, '.'); {
			tail = tail[strings.IndexByte(tail, '.')+1:]
			labels--
			want(trafficTreeLockKey(tail), labels, false)
		}
	}
	for _, host := range hosts {
		if host = normalizeTrafficLockName(host); host != "" {
			want(trafficHostLockPrefix+host, strings.Count(host, ".")+1, true)
			ancestors(host)
		}
	}
	for _, subtree := range subtrees {
		subtree = normalizeTrafficLockName(subtree)
		labels := 0
		if subtree != "" {
			labels = strings.Count(subtree, ".") + 1
		}
		want(trafficTreeLockKey(subtree), labels, true)
		ancestors(subtree)
	}
	locks := make([]trafficSessionLock, 0, len(shared))
	for key, isShared := range shared {
		locks = append(locks, trafficSessionLock{Key: key, Shared: isShared})
	}
	sort.Slice(locks, func(i, j int) bool {
		if depth[locks[i].Key] != depth[locks[j].Key] {
			return depth[locks[i].Key] < depth[locks[j].Key]
		}
		return locks[i].Key < locks[j].Key
	})
	return locks
}
