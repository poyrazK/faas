package globaldirectory

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Caller holds s.mu; PostgreSQL locks the authoritative tenant rows instead.
func (s *MemoryStore) quotaAuthority(ctx context.Context, owner, actor string, expected api.Plan) error {
	o, ok := s.owners[owner]
	if !ok || s.orgAuthority == nil {
		return ErrNotFound
	}
	if o.Shared() {
		if err := s.orgTargetAuthority(ctx, owner, actor); err != nil {
			return err
		}
		org, err := activeSharedOrg(ctx, s.orgAuthority, o.OrgID)
		if err != nil {
			return err
		}
		if expected != "" && org.Plan != expected {
			return ErrOwnerPlanChanged
		}
		return nil
	}
	if actor != o.AccountID {
		return ErrOwnerForbidden
	}
	a, err := s.orgAuthority.AccountByID(ctx, actor)
	if errors.Is(err, state.ErrNotFound) {
		return ErrOwnerForbidden
	}
	if err != nil {
		return err
	}
	if !a.MayDeploy() || !a.EmailVerified() {
		return ErrOwnerForbidden
	}
	if expected != "" && a.Plan != expected {
		return ErrOwnerPlanChanged
	}
	return nil
}

func (s *MemoryStore) ReserveRuntimeQuota(ctx context.Context, req RuntimeQuotaRequest) (RuntimeQuotaAllocation, error) {
	req, policy, err := canonicalQuotaRequest(req)
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.quotaAuthority(ctx, req.OwnerID, req.ActorID, req.ExpectedPlan); err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	app, err := s.app(req.OwnerID, req.Slug)
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	if old, ok := s.runtimeQuotas[app.ID][req.AllocationID]; ok {
		if !quotaInputMatches(old, req, policy) {
			return RuntimeQuotaAllocation{}, ErrConflict
		}
		return copyQuota(old), nil
	}
	if len(req.Shares) != len(s.targets[app.ID]) {
		return RuntimeQuotaAllocation{}, ErrConflict
	}
	if s.runtimeQuotaHeads[app.ID] >= api.GlobalRuntimeQuotaGenerationMax {
		return RuntimeQuotaAllocation{}, ErrRuntimeQuotaLimit
	}
	now := time.Now().UTC()
	a := RuntimeQuotaAllocation{GlobalAppID: app.ID, OwnerID: app.OwnerID, AllocationID: req.AllocationID, Generation: s.runtimeQuotaHeads[app.ID] + 1, Policy: policy, CreatedAt: now}
	steady, peak := int64(0), int64(0)
	for _, old := range s.runtimeQuotas[app.ID] {
		for _, target := range old.Targets {
			if target.ReleasedAt == nil {
				steady += int64(target.Concurrency)
				peak += int64(target.Concurrency + target.RolloutConcurrency)
			}
		}
	}
	for _, share := range req.Shares {
		t, ok := s.targets[app.ID][share.Region]
		if !ok {
			return RuntimeQuotaAllocation{}, ErrConflict
		}
		cell := s.cells[t.Region]
		t.APIBase, t.CellEnabled, t.RegistryGeneration = cell.APIBase, cell.Enabled, cell.Generation
		if !t.Resolvable() {
			return RuntimeQuotaAllocation{}, ErrStaleCell
		}
		target := RuntimeQuotaTarget{Target: t, Concurrency: share.Concurrency}
		if share.Region == req.RolloutRegion {
			target.RolloutConcurrency = api.RolloutConcurrencyGrant
		}
		a.Targets = append(a.Targets, target)
		steady += int64(target.Concurrency)
		peak += int64(target.Concurrency + target.RolloutConcurrency)
	}
	if steady > int64(policy.Concurrency) {
		return RuntimeQuotaAllocation{}, &RuntimeQuotaLimitError{Kind: "concurrency", Limit: int64(policy.Concurrency), Observed: steady}
	}
	if peak > int64(policy.PeakConcurrency) {
		return RuntimeQuotaAllocation{}, &RuntimeQuotaLimitError{Kind: "peak", Limit: int64(policy.PeakConcurrency), Observed: peak}
	}
	if s.runtimeQuotas[app.ID] == nil {
		s.runtimeQuotas[app.ID] = make(map[string]RuntimeQuotaAllocation)
	}
	s.runtimeQuotas[app.ID][a.AllocationID] = copyQuota(a)
	s.runtimeQuotaHeads[app.ID] = a.Generation
	return copyQuota(a), nil
}

func (s *MemoryStore) quota(owner, slug, id string) (RuntimeQuotaAllocation, error) {
	app, err := s.app(owner, slug)
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	a, ok := s.runtimeQuotas[app.ID][id]
	if !ok {
		return RuntimeQuotaAllocation{}, ErrNotFound
	}
	return a, nil
}

func (s *MemoryStore) RuntimeQuota(_ context.Context, owner, slug, id string) (RuntimeQuotaAllocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.quota(owner, slug, id)
	return copyQuota(a), err
}

func (s *MemoryStore) IssueRuntimeQuotaTarget(ctx context.Context, owner, slug, id, region string) (RuntimeQuotaTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.quota(owner, slug, id)
	if err != nil {
		return RuntimeQuotaTarget{}, err
	}
	policy, err := s.currentQuotaPolicy(ctx, owner)
	if err != nil {
		return RuntimeQuotaTarget{}, err
	}
	if policy != a.Policy {
		return RuntimeQuotaTarget{}, ErrOwnerPlanChanged
	}
	if a.RetiredAt != nil {
		return RuntimeQuotaTarget{}, ErrRuntimeQuotaRetired
	}
	for i, target := range a.Targets {
		if target.Region != region {
			continue
		}
		current, ok := s.targets[a.GlobalAppID][region]
		cell := s.cells[region]
		current.APIBase, current.CellEnabled, current.RegistryGeneration = cell.APIBase, cell.Enabled, cell.Generation
		if !ok || !snapshotMatches(target.Target, current) {
			return RuntimeQuotaTarget{}, ErrStaleCell
		}
		if target.IssuedAt == nil {
			now := time.Now().UTC()
			a.Targets[i].IssuedAt = &now
			s.runtimeQuotas[a.GlobalAppID][id] = copyQuota(a)
		}
		return copyQuota(a).Targets[i], nil
	}
	return RuntimeQuotaTarget{}, ErrNotFound
}

func (s *MemoryStore) currentQuotaPolicy(ctx context.Context, owner string) (RuntimeQuotaPolicy, error) {
	o, ok := s.owners[owner]
	if !ok || s.orgAuthority == nil {
		return RuntimeQuotaPolicy{}, ErrNotFound
	}
	if o.Shared() {
		org, err := activeSharedOrg(ctx, s.orgAuthority, o.OrgID)
		if err != nil {
			return RuntimeQuotaPolicy{}, err
		}
		return runtimeQuotaPolicy(org.Plan)
	}
	a, err := s.orgAuthority.AccountByID(ctx, o.AccountID)
	if err != nil {
		return RuntimeQuotaPolicy{}, err
	}
	if !a.MayDeploy() || !a.EmailVerified() {
		return RuntimeQuotaPolicy{}, ErrOwnerInactive
	}
	return runtimeQuotaPolicy(a.Plan)
}

func (s *MemoryStore) RetireRuntimeQuota(ctx context.Context, owner, actor, slug, id string) (RuntimeQuotaAllocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.quotaAuthority(ctx, owner, actor, ""); err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	a, err := s.quota(owner, slug, id)
	if err != nil {
		return RuntimeQuotaAllocation{}, err
	}
	if a.RetiredAt == nil {
		now := time.Now().UTC()
		a.RetirementID, a.RetiredAt = uuid.NewString(), &now
		for i, target := range a.Targets {
			if target.IssuedAt == nil {
				a.Targets[i].ReleasedAt = &now
			}
		}
		s.runtimeQuotas[a.GlobalAppID][id] = copyQuota(a)
	}
	return copyQuota(a), nil
}

func (s *MemoryStore) ConfirmRuntimeQuotaRetirement(_ context.Context, owner, slug string, ack RuntimeQuotaRetirement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.quota(owner, slug, ack.AllocationID)
	if err != nil {
		return err
	}
	for i, target := range a.Targets {
		if target.Region != ack.Binding.Region {
			continue
		}
		if !quotaRetirementMatches(a, target, ack) {
			return ErrRuntimeQuotaIdentity
		}
		if target.ReleasedAt == nil {
			now := time.Now().UTC()
			a.Targets[i].ReleasedAt = &now
			s.runtimeQuotas[a.GlobalAppID][a.AllocationID] = copyQuota(a)
		}
		return nil
	}
	return ErrRuntimeQuotaIdentity
}

var _ RuntimeQuotaStore = (*MemoryStore)(nil)
