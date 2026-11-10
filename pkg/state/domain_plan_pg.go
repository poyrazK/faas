package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) readDomainPlan(ctx context.Context, reader sqlc.DBTX, in DomainPlanInput) (api.DomainPlan, []domainPlanClaim, error) {
	var targets []domainPlanTarget
	var claims []domainPlanClaim
	q := sqlc.New()
	for i, spec := range in.Manifest.Domains {
		target := in.Targets[i]
		scope := target.Environment
		if scope == "" {
			scope = DefaultEnvScope
		}
		data, err := q.ReadDomainPlanTarget(ctx, reader, sqlc.ReadDomainPlanTargetParams{AppID: target.AppID, EnvironmentID: target.EnvironmentID, Scope: scope})
		if err != nil {
			return api.DomainPlan{}, nil, mapErr(err)
		}
		var t domainPlanTarget
		if err := json.Unmarshal(data, &t); err != nil {
			return api.DomainPlan{}, nil, err
		}
		targets = append(targets, t)
		var c domainPlanClaim
		data, err = q.ReadDomainPlanClaim(ctx, reader, spec.Domain)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return api.DomainPlan{}, nil, err
		}
		if err == nil {
			if err := json.Unmarshal(data, &c); err != nil {
				return api.DomainPlan{}, nil, err
			}
		}
		c.TenantOverride = c.TenantOverride && customDomainTenantRouting(ctx)
		claims = append(claims, c)
	}
	p, err := buildDomainBindingPlan(in, targets, claims)
	if err != nil {
		return p, nil, err
	}
	subject, err := q.ReadDomainPlanBudgetSubject(ctx, reader, in.Targets[0].AppID)
	if err != nil {
		return p, nil, err
	}
	rows, err := q.ReadDomainPlanBudget(ctx, reader, subject)
	if err != nil {
		return p, nil, err
	}
	pending := make(map[string]int)
	for _, r := range rows {
		pending[r.AppID] = int(r.Pending)
	}
	return p, claims, checkDomainPlanBudget(in, p, pending)
}

func (s *PgStore) PlanDomainBindings(ctx context.Context, in DomainPlanInput) (api.DomainPlan, error) {
	if err := validateDomainPlanInput(in); err != nil {
		return api.DomainPlan{}, err
	}
	// A read-only transaction captures the whole manifest at one snapshot.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.DomainPlan{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	p, _, err := s.readDomainPlan(ctx, tx, in)
	return p, err
}

func (s *PgStore) ApplyDomainBindings(ctx context.Context, in DomainPlanInput, expected string) (api.DomainPlan, []CustomDomain, error) {
	if err := validateDomainPlanInput(in); err != nil {
		return api.DomainPlan{}, nil, err
	}
	q := sqlc.New()
	subject, err := q.ReadDomainPlanBudgetSubject(ctx, s.pool, in.Targets[0].AppID)
	if err != nil {
		return api.DomainPlan{}, nil, mapErr(err)
	}
	var hosts []string
	for _, d := range in.Manifest.Domains {
		hosts = append(hosts, d.Domain)
	}
	tx, err := s.beginTrafficBinding(ctx, TrafficPolicyWriterDomainBinding, in.AccountID, hosts, "", subject)
	if err != nil {
		return api.DomainPlan{}, nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Lock source and destination apps in a stable order. Deployment and
	// environment writers also use the capacity/account fences held above.
	ids := make(map[string]bool)
	for _, t := range in.Targets {
		ids[t.AppID] = true
	}
	for _, c := range tx.before.Domains {
		for _, h := range hosts {
			if c.Domain == h {
				ids[c.App] = true
			}
		}
	}
	appIDs := make([]pgtype.UUID, 0, len(ids))
	for id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return api.DomainPlan{}, nil, ErrInvalidArgument
		}
		appIDs = append(appIDs, pgtype.UUID{Bytes: parsed, Valid: true})
	}
	sort.Slice(appIDs, func(i, j int) bool { return pgUUIDString(appIDs[i]) < pgUUIDString(appIDs[j]) })
	if _, err := q.LockDomainPlanApps(ctx, tx, appIDs); err != nil {
		return api.DomainPlan{}, nil, mapErr(err)
	}
	currentSubject, err := q.ReadDomainPlanBudgetSubject(ctx, tx, in.Targets[0].AppID)
	if err != nil || currentSubject != subject {
		return api.DomainPlan{}, nil, ErrConflict
	}
	p, claims, err := s.readDomainPlan(ctx, tx, in)
	if err != nil {
		return p, nil, err
	}
	if p.SHA256 != expected {
		return p, nil, fmt.Errorf("%w: reviewed domain plan is stale; generate and review a new plan", ErrConflict)
	}
	ctx = WithPublicCustomDomainClaim(ctx)
	for i, c := range p.Changes {
		if c.Action == "noop" {
			continue
		}
		if err := checkPublicCustomDomainClaim(ctx, tx, c.Domain, c.After.AppID); err != nil {
			return p, nil, err
		}
		if c.Action == "create" {
			err = q.InsertDomainPlanClaim(ctx, tx, sqlc.InsertDomainPlanClaimParams{Domain: c.Domain, AppID: c.After.AppID, EnvironmentID: c.After.EnvironmentID, Token: uuid.NewString()})
		} else {
			var count int64
			count, err = q.MoveDomainPlanClaim(ctx, tx, sqlc.MoveDomainPlanClaimParams{Domain: c.Domain, AppID: c.After.AppID, EnvironmentID: c.After.EnvironmentID, BeforeAppID: claims[i].Claim.AppID, Token: claims[i].Claim.ChallengeToken})
			if err == nil && count != 1 {
				err = ErrConflict
			}
			if err == nil {
				err = q.ClearMovedDomainDefault(ctx, tx, c.Domain)
			}
			if err == nil {
				err = q.ClearCustomDomainProbeObservation(ctx, tx, c.Domain)
			}
		}
		if err != nil {
			return p, nil, mapErr(err)
		}
	}
	// Return the committed snapshot, including preserved certificate state.
	_, after, err := s.readDomainPlan(ctx, tx, in)
	if err != nil {
		return p, nil, err
	}
	result := make([]CustomDomain, 0, len(after))
	for _, c := range after {
		result = append(result, c.Claim)
	}
	if err := tx.Commit(ctx); err != nil {
		return p, nil, mapErr(err)
	}
	return p, result, nil
}

var _ DomainPlanStore = (*PgStore)(nil)
