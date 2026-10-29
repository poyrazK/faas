// adr: 20261027163626 — the hostnames a binding write declares, audited.
package state

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// trafficBindingScope is what a binding write may change: exact hostnames
// and subtrees (a wildcard "*.s" is the subtree s). The scoped guard will
// lock and analyse only this scope, so every hostname whose claim or
// reservation the write changes must lie inside it. Until the guard is
// switched on, writes only audit the declaration against what changed.
type trafficBindingScope struct {
	Hosts, Subtrees []string
}

// Kinds of a changed hostname the declaration missed. Labels are closed.
const (
	TrafficBindingScopeKindDomain             = "domain"
	TrafficBindingScopeKindTenant             = "tenant"
	TrafficBindingScopeKindAlias              = "alias"
	TrafficBindingScopeKindPrimary            = "primary"
	TrafficBindingScopeKindReservationPrimary = "reservation_primary"
	TrafficBindingScopeKindReservationDomain  = "reservation_domain"
	TrafficBindingScopeKindReservationTenant  = "reservation_tenant"
	TrafficBindingScopeKindReservationOther   = "reservation_other"
)

func trafficReservationScopeKind(kind string) string {
	switch kind {
	case "primary":
		return TrafficBindingScopeKindReservationPrimary
	case "domain":
		return TrafficBindingScopeKindReservationDomain
	case "tenant":
		return TrafficBindingScopeKindReservationTenant
	default:
		return TrafficBindingScopeKindReservationOther
	}
}

// TrafficBindingScopeObserver receives one audited binding write: its writer,
// and the kind of every changed hostname outside the declared scope (empty
// when the declaration covered the change).
type TrafficBindingScopeObserver func(writer string, missed []string)

var trafficBindingScopeObserver atomic.Pointer[TrafficBindingScopeObserver]

// SetTrafficBindingScopeObserver wires the daemon's counter; nil disables it.
func SetTrafficBindingScopeObserver(observe TrafficBindingScopeObserver) {
	if observe == nil {
		trafficBindingScopeObserver.Store(nil)
		return
	}
	trafficBindingScopeObserver.Store(&observe)
}

func observeTrafficBindingScope(writer string, missed []string) {
	if observe := trafficBindingScopeObserver.Load(); observe != nil {
		(*observe)(writer, missed)
	}
}

func (s *trafficBindingScope) add(host string) {
	host = normalizeTrafficLockName(host)
	if suffix, wild := WildcardDomainSuffix(host); wild {
		s.Subtrees = append(s.Subtrees, suffix)
	} else if host != "" {
		s.Hosts = append(s.Hosts, host)
	}
}

func (s *trafficBindingScope) normalize() {
	sort.Strings(s.Hosts)
	sort.Strings(s.Subtrees)
	s.Hosts, s.Subtrees = slices.Compact(s.Hosts), slices.Compact(s.Subtrees)
}

// covers reports whether a changed hostname or subtree lies in the scope.
func (s trafficBindingScope) covers(host string, subtree bool) bool {
	if !subtree && slices.Contains(s.Hosts, host) || subtree && slices.Contains(s.Subtrees, host) {
		return true
	}
	for _, root := range s.Subtrees {
		if root == "" || strings.HasSuffix(host, "."+root) {
			return true
		}
	}
	return false
}

// declareTrafficBindingScope is the scope a binding write declares from its
// account's claims, the hostnames it names, and the synthetic hostnames of
// its app (or of every app of its account when it names none).
func declareTrafficBindingScope(ctx context.Context, reader sqlc.DBTX, claims trafficBindingClaims, account string, requested []string, appID, appsSuffix string) (trafficBindingScope, error) {
	var scope trafficBindingScope
	for _, host := range trafficBindingAffectedHosts(claims, account, requested, appID) {
		scope.add(host)
	}
	app := pgtype.UUID{}
	if appID != "" {
		app = uuidToPgtype(appID)
	}
	hosts, err := sqlc.New().ReadTrafficAppBindingHosts(ctx, reader, sqlc.ReadTrafficAppBindingHostsParams{
		AppsSuffix: appsSuffix, AppID: app, AccountID: uuidToPgtype(account), MaxInputs: api.TrafficPolicyMaxAnalysisInputs,
	})
	if err != nil {
		return scope, mapErr(err)
	}
	if len(hosts) > api.TrafficPolicyMaxAnalysisInputs {
		return scope, analysisLimit("inputs", "hosts", api.TrafficPolicyMaxAnalysisInputs, int64(len(hosts)))
	}
	for _, host := range hosts {
		scope.add(host)
	}
	scope.normalize()
	return scope, nil
}

type trafficBindingChange struct {
	host, kind     string
	subtree, added bool
}

// trafficBindingChanges lists every hostname whose claims or reservations
// differ between two snapshots, with the kind of the first difference.
func trafficBindingChanges(before, after trafficBindingClaims, beforeReservations, afterReservations []trafficHostReservation) []trafficBindingChange {
	records := func(claims trafficBindingClaims, reservations []trafficHostReservation) map[string][]string {
		byHost := map[string][]string{}
		add := func(kind, host string, claim any) {
			encoded, _ := json.Marshal(claim)
			key := kind + "\x00" + normalizeTrafficLockName(host)
			byHost[key] = append(byHost[key], string(encoded))
		}
		for _, claim := range claims.Domains {
			add(TrafficBindingScopeKindDomain, claim.Domain, claim)
		}
		for _, claim := range claims.Tenants {
			add(TrafficBindingScopeKindTenant, claim.Host, claim)
		}
		for _, claim := range claims.Aliases {
			add(TrafficBindingScopeKindAlias, claim.Host, claim)
		}
		for _, claim := range claims.Primaries {
			add(TrafficBindingScopeKindPrimary, claim.Host, claim)
		}
		for _, reservation := range reservations {
			add(trafficReservationScopeKind(reservation.Kind), reservation.Host, reservation)
		}
		for key := range byHost {
			sort.Strings(byHost[key])
		}
		return byHost
	}
	prior, next := records(before, beforeReservations), records(after, afterReservations)
	keys := make([]string, 0, len(prior)+len(next))
	for key := range prior {
		keys = append(keys, key)
	}
	for key := range next {
		if _, seen := prior[key]; !seen {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var changes []trafficBindingChange
	for _, key := range keys {
		if slices.Equal(prior[key], next[key]) {
			continue
		}
		kind, host, _ := strings.Cut(key, "\x00")
		_, existed := prior[key]
		change := trafficBindingChange{host: host, kind: kind, added: !existed}
		if suffix, wild := WildcardDomainSuffix(host); wild {
			change.host, change.subtree = suffix, true
		}
		changes = append(changes, change)
	}
	return changes
}

func (s trafficBindingScope) equal(other trafficBindingScope) bool {
	return slices.Equal(s.Hosts, other.Hosts) && slices.Equal(s.Subtrees, other.Subtrees)
}

// auditTrafficBindingScope reports and returns the kinds of changes the scope
// missed.
// A hostname newly claimed or reserved need not be declared: it only takes
// that exact hostname away from global routes and other owners, and the
// writer's own account check covers what it now serves there. A new subtree
// reaches other hostnames, so it must be declared like every removal and
// change.
func auditTrafficBindingScope(writer string, scope trafficBindingScope, changes []trafficBindingChange) []string {
	var missed []string
	for _, change := range changes {
		if change.added && !change.subtree {
			continue
		}
		if !scope.covers(change.host, change.subtree) {
			missed = append(missed, change.kind)
		}
	}
	observeTrafficBindingScope(writer, missed)
	return missed
}
