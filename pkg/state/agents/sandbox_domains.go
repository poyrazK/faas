package agents

// A web-mode sandbox session's domain log (ADR-20261117091026 §4): the
// domains vmmd granted it, or refused and why, each once.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// SandboxDomain is one entry of a session's domain log. Refused is empty
// for a grant, else "policy", "budget" or "platform".
type SandboxDomain struct {
	Domain    string
	Refused   string
	FirstSeen time.Time
}

var sandboxDomainRefusals = map[string]bool{"": true, "policy": true, "budget": true, "platform": true}

// RecordSandboxDomains adds entries to a session's log while this replica
// holds its lease, and returns how many it added. It keeps at most the
// plan's SandboxWebDistinctDomains grants and SandboxEgressRefusedLogMax
// refusals, drops entries it cannot keep (malformed ones included), and an
// entry already logged keeps its first sighting.
func (s *Store) RecordSandboxDomains(ctx context.Context, id, token string, plan api.Plan, entries []SandboxDomain) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	have, err := s.q.CountSandboxSessionDomains(ctx, s.pool, id)
	if err != nil {
		return 0, fmt.Errorf("record session domains: %w", err)
	}
	grants, refusals := 0, api.SandboxEgressRefusedLogMax-int(have.Refused)
	if plan.Valid() {
		grants = api.SandboxWebDistinctDomains[plan.PlanIndex()] - int(have.Granted)
	}
	var p sqlc.RecordSandboxSessionDomainsParams
	seen := make(map[[2]string]bool, len(entries))
	for _, e := range entries {
		key := [2]string{e.Refused, e.Domain}
		if e.Domain == "" || len(e.Domain) > 253 || !sandboxDomainRefusals[e.Refused] || seen[key] {
			continue
		}
		budget := &grants
		if e.Refused != "" {
			budget = &refusals
		}
		if *budget <= 0 {
			continue
		}
		*budget--
		seen[key] = true
		p.Domains, p.Refused = append(p.Domains, e.Domain), append(p.Refused, e.Refused)
		p.FirstSeen = append(p.FirstSeen, pgtype.Timestamptz{Time: e.FirstSeen, Valid: true})
	}
	if len(p.Domains) == 0 {
		return 0, nil
	}
	p.SessionID, p.LeaseToken = id, token
	n, err := s.q.RecordSandboxSessionDomains(ctx, s.pool, p)
	if err != nil {
		return 0, fmt.Errorf("record session domains: %w", err)
	}
	return int(n), nil
}

// SandboxDomains returns a session's domain log, oldest first, for its
// account.
func (s *Store) SandboxDomains(ctx context.Context, accountID, id string) ([]SandboxDomain, error) {
	rows, err := s.q.ListSandboxSessionDomains(ctx, s.pool, sqlc.ListSandboxSessionDomainsParams{SessionID: id, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("list session domains: %w", err)
	}
	out := make([]SandboxDomain, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxDomain{Domain: r.Domain, Refused: r.Refused, FirstSeen: r.FirstSeen.Time})
	}
	return out, nil
}
