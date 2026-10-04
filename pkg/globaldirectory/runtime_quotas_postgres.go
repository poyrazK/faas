package globaldirectory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func quotaTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func registerQuotaPolicy(ctx context.Context, tx pgx.Tx, p RuntimeQuotaPolicy) error {
	q := &sqlc.Queries{}
	if err := q.RegisterGlobalRuntimeQuotaPolicy(ctx, tx, sqlc.RegisterGlobalRuntimeQuotaPolicyParams{PolicyID: p.ID, Plan: string(p.Plan), MaxConcurrency: int32(p.Concurrency), MaxPeakConcurrency: int32(p.PeakConcurrency)}); err != nil {
		return err
	}
	stored, err := q.ReadGlobalRuntimeQuotaPolicy(ctx, tx, sqlc.ReadGlobalRuntimeQuotaPolicyParams{PolicyID: p.ID, Plan: string(p.Plan)})
	if err != nil {
		return err
	}
	if int(stored.MaxConcurrency) != p.Concurrency || int(stored.MaxPeakConcurrency) != p.PeakConcurrency {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) readRuntimeQuota(ctx context.Context, db sqlc.DBTX, owner, slug, id string) (RuntimeQuotaAllocation, error) {
	q := &sqlc.Queries{}
	r, err := q.ReadGlobalRuntimeQuotaAllocation(ctx, db, sqlc.ReadGlobalRuntimeQuotaAllocationParams{OwnerID: idArg(owner), Slug: slug, AllocationID: idArg(id)})
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	a := RuntimeQuotaAllocation{GlobalAppID: r.GlobalAppID, OwnerID: r.OwnerID, AllocationID: r.AllocationID, Generation: r.Generation, Policy: RuntimeQuotaPolicy{ID: r.PolicyID, Plan: api.Plan(r.Plan), Concurrency: int(r.MaxConcurrency), PeakConcurrency: int(r.MaxPeakConcurrency)}, CreatedAt: r.CreatedAt.Time, RetirementID: r.RetirementID, RetiredAt: quotaTime(r.RetiredAt)}
	rows, err := q.ListGlobalRuntimeQuotaTargets(ctx, db, sqlc.ListGlobalRuntimeQuotaTargetsParams{GlobalAppID: idArg(a.GlobalAppID), AllocationID: idArg(id)})
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	for _, row := range rows {
		var target Target
		if err = json.Unmarshal(row.Snapshot, &target); err != nil {
			return RuntimeQuotaAllocation{}, err
		}
		a.Targets = append(a.Targets, RuntimeQuotaTarget{Target: target, Concurrency: int(row.Concurrency), RolloutConcurrency: int(row.RolloutConcurrency), IssuedAt: quotaTime(row.IssuedAt), ReleasedAt: quotaTime(row.ReleasedAt)})
	}
	return a, nil
}

func (s *PgStore) ReserveRuntimeQuota(ctx context.Context, req RuntimeQuotaRequest) (RuntimeQuotaAllocation, error) {
	req, p, err := canonicalQuotaRequest(req)
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	// Lowercase field names are an internal SQL protocol, independent of the SDK.
	shares := make([]map[string]any, 0, len(req.Shares))
	for _, share := range req.Shares {
		shares = append(shares, map[string]any{"region": share.Region, "concurrency": share.Concurrency})
	}
	data, err := json.Marshal(shares)
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	var out RuntimeQuotaAllocation
	err = s.transaction(ctx, "reserve runtime quota", func(tx pgx.Tx) error {
		if err := registerQuotaPolicy(ctx, tx, p); err != nil {
			return err
		}
		_, err := (&sqlc.Queries{}).ReserveGlobalRuntimeQuota(ctx, tx, sqlc.ReserveGlobalRuntimeQuotaParams{OwnerID: idArg(req.OwnerID), ActorID: idArg(req.ActorID), Slug: req.Slug, AllocationID: idArg(req.AllocationID), ExpectedPlan: string(req.ExpectedPlan), PolicyID: p.ID, Shares: data, RolloutRegion: req.RolloutRegion})
		if err != nil {
			return err
		}
		out, err = s.readRuntimeQuota(ctx, tx, req.OwnerID, req.Slug, req.AllocationID)
		return err
	})
	return out, err
}

func lockQuotaApp(ctx context.Context, tx pgx.Tx, owner, slug string) error {
	_, err := (&sqlc.Queries{}).LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(owner), Slug: slug})
	return err
}

func (s *PgStore) RuntimeQuota(ctx context.Context, owner, slug, id string) (RuntimeQuotaAllocation, error) {
	var out RuntimeQuotaAllocation
	err := s.transaction(ctx, "read runtime quota", func(tx pgx.Tx) error {
		if err := lockQuotaApp(ctx, tx, owner, slug); err != nil {
			return err
		}
		var err error
		out, err = s.readRuntimeQuota(ctx, tx, owner, slug, id)
		return err
	})
	return out, err
}

func (s *PgStore) checkQuotaSnapshot(ctx context.Context, tx pgx.Tx, target RuntimeQuotaTarget) error {
	q := &sqlc.Queries{}
	cell, err := q.LockGlobalDirectoryCell(ctx, tx, sqlc.LockGlobalDirectoryCellParams{CellID: idArg(target.CellID), Generation: target.CellGeneration})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleCell
	}
	if err != nil {
		return err
	}
	r, err := q.CurrentGlobalReleaseBinding(ctx, tx, sqlc.CurrentGlobalReleaseBindingParams{GlobalAppID: idArg(target.GlobalAppID), Region: target.Region})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStaleCell
	}
	if err != nil {
		return err
	}
	b := Binding{CellID: r.CellID, Region: target.Region, CellGeneration: r.CellGeneration, RegionalAccountID: r.RegionalAccountID, RegionalOrgID: r.RegionalOrgID, RegionalAppID: r.RegionalAppID, RegionalAppSlug: r.RegionalAppSlug}
	if b != target.Binding || cell.ApiBase != target.APIBase {
		return ErrStaleCell
	}
	return nil
}

func (s *PgStore) IssueRuntimeQuotaTarget(ctx context.Context, owner, slug, id, region string) (RuntimeQuotaTarget, error) {
	var out RuntimeQuotaTarget
	err := s.transaction(ctx, "issue runtime quota", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		current, err := q.LockGlobalRuntimeQuotaTenant(ctx, tx, idArg(owner))
		if err != nil {
			return err
		}
		policy, err := runtimeQuotaPolicy(api.Plan(current))
		if err != nil {
			return err
		}
		if err = lockQuotaApp(ctx, tx, owner, slug); err != nil {
			return err
		}
		a, err := s.readRuntimeQuota(ctx, tx, owner, slug, id)
		if err != nil {
			return err
		}
		if a.Policy != policy {
			return ErrOwnerPlanChanged
		}
		if a.RetiredAt != nil {
			return ErrRuntimeQuotaRetired
		}
		for _, target := range a.Targets {
			if target.Region != region {
				continue
			}
			if err = s.checkQuotaSnapshot(ctx, tx, target); err != nil {
				return err
			}
			if target.IssuedAt == nil {
				if _, err = q.IssueGlobalRuntimeQuotaTarget(ctx, tx, sqlc.IssueGlobalRuntimeQuotaTargetParams{GlobalAppID: idArg(a.GlobalAppID), AllocationID: idArg(id), Region: region}); err != nil {
					return err
				}
				a, err = s.readRuntimeQuota(ctx, tx, owner, slug, id)
				if err != nil {
					return err
				}
			}
			for _, t := range a.Targets {
				if t.Region == region {
					out = t
					return nil
				}
			}
		}
		return ErrNotFound
	})
	return out, err
}

func (s *PgStore) RetireRuntimeQuota(ctx context.Context, owner, actor, slug, id string) (RuntimeQuotaAllocation, error) {
	var out RuntimeQuotaAllocation
	err := s.transaction(ctx, "retire runtime quota", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		if _, err := q.LockGlobalRuntimeQuotaOwner(ctx, tx, sqlc.LockGlobalRuntimeQuotaOwnerParams{OwnerID: idArg(owner), ActorID: idArg(actor)}); err != nil {
			return err
		}
		if err := lockQuotaApp(ctx, tx, owner, slug); err != nil {
			return err
		}
		a, err := s.readRuntimeQuota(ctx, tx, owner, slug, id)
		if err != nil {
			return err
		}
		if a.RetiredAt == nil {
			if _, err = q.RetireGlobalRuntimeQuotaAllocation(ctx, tx, sqlc.RetireGlobalRuntimeQuotaAllocationParams{GlobalAppID: idArg(a.GlobalAppID), AllocationID: idArg(id), RetirementID: idArg(uuid.NewString())}); err != nil {
				return err
			}
			if err = q.ReleaseUnissuedGlobalRuntimeQuotaTargets(ctx, tx, sqlc.ReleaseUnissuedGlobalRuntimeQuotaTargetsParams{GlobalAppID: idArg(a.GlobalAppID), AllocationID: idArg(id)}); err != nil {
				return err
			}
		}
		out, err = s.readRuntimeQuota(ctx, tx, owner, slug, id)
		return err
	})
	return out, err
}

func (s *PgStore) ConfirmRuntimeQuotaRetirement(ctx context.Context, owner, slug string, ack RuntimeQuotaRetirement) error {
	return s.transaction(ctx, "confirm runtime quota retirement", func(tx pgx.Tx) error {
		if err := lockQuotaApp(ctx, tx, owner, slug); err != nil {
			return err
		}
		a, err := s.readRuntimeQuota(ctx, tx, owner, slug, ack.AllocationID)
		if err != nil {
			return err
		}
		for _, target := range a.Targets {
			if target.Region != ack.Binding.Region {
				continue
			}
			if !quotaRetirementMatches(a, target, ack) {
				return ErrRuntimeQuotaIdentity
			}
			if target.ReleasedAt != nil {
				return nil
			}
			data, err := json.Marshal(ack)
			if err != nil {
				return err
			}
			n, err := (&sqlc.Queries{}).ConfirmGlobalRuntimeQuotaDrain(ctx, tx, sqlc.ConfirmGlobalRuntimeQuotaDrainParams{GlobalAppID: idArg(a.GlobalAppID), AllocationID: idArg(a.AllocationID), Region: target.Region, Confirmation: data})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrRuntimeQuotaIdentity
			}
			return nil
		}
		return ErrRuntimeQuotaIdentity
	})
}

var _ RuntimeQuotaStore = (*PgStore)(nil)
