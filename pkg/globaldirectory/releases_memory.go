package globaldirectory

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func copyRelease(r Release) Release {
	r.Targets = append([]ReleaseTarget(nil), r.Targets...)
	return r
}

func (s *MemoryStore) PlanRelease(_ context.Context, owner, slug, releaseID, image string) (Release, error) {
	if !ValidReleaseInput(releaseID, image) {
		return Release{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.app(owner, slug)
	if err != nil {
		return Release{}, err
	}
	if old, ok := s.releases[app.ID][releaseID]; ok {
		if old.Image != image {
			return Release{}, ErrConflict
		}
		old.DesiredGeneration = s.releaseHeads[app.ID]
		return copyRelease(old), nil
	}
	if len(s.targets[app.ID]) == 0 {
		return Release{}, ErrNoTargets
	}
	if len(s.targets[app.ID]) > api.GlobalImageReleaseTargetsMax {
		return Release{}, &ReleaseLimitError{Kind: "targets", Limit: api.GlobalImageReleaseTargetsMax, Observed: int64(len(s.targets[app.ID]))}
	}
	if s.releaseHeads[app.ID] >= api.GlobalImageReleaseGenerationMax {
		return Release{}, &ReleaseLimitError{Kind: "generation", Limit: api.GlobalImageReleaseGenerationMax, Observed: s.releaseHeads[app.ID] + 1}
	}
	r := Release{GlobalAppID: app.ID, AccountID: app.AccountID, ReleaseID: releaseID, Generation: s.releaseHeads[app.ID] + 1, Image: image, CreatedAt: time.Now().UTC()}
	r.DesiredGeneration = r.Generation
	for _, target := range s.targets[app.ID] {
		cell := s.cells[target.Region]
		target.APIBase, target.CellEnabled, target.RegistryGeneration = cell.APIBase, cell.Enabled, cell.Generation
		if !target.Resolvable() {
			return Release{}, ErrStaleCell
		}
		r.Targets = append(r.Targets, ReleaseTarget{Target: target, Outcome: ReleasePending, UpdatedAt: r.CreatedAt})
	}
	sort.Slice(r.Targets, func(i, j int) bool { return r.Targets[i].Region < r.Targets[j].Region })
	if s.releases[app.ID] == nil {
		s.releases[app.ID] = make(map[string]Release)
	}
	s.releases[app.ID][releaseID] = copyRelease(r)
	s.releaseHeads[app.ID] = r.Generation
	s.wake(app)
	return r, nil
}

func (s *MemoryStore) release(owner, slug, releaseID string) (Release, error) {
	app, err := s.app(owner, slug)
	if err != nil {
		return Release{}, err
	}
	r, ok := s.releases[app.ID][releaseID]
	if !ok {
		return Release{}, ErrNotFound
	}
	r.DesiredGeneration = s.releaseHeads[app.ID]
	return r, nil
}

func (s *MemoryStore) GetRelease(_ context.Context, owner, slug, releaseID string) (Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.release(owner, slug, releaseID)
	return copyRelease(r), err
}

func (s *MemoryStore) checkReleaseTarget(r Release, region string) error {
	if r.Generation != r.DesiredGeneration {
		return ErrSuperseded
	}
	for _, target := range r.Targets {
		if target.Region != region {
			continue
		}
		current, ok := s.targets[r.GlobalAppID][region]
		if !ok {
			return ErrStaleCell
		}
		cell := s.cells[region]
		current.APIBase, current.CellEnabled, current.RegistryGeneration = cell.APIBase, cell.Enabled, cell.Generation
		if !snapshotMatches(target.Target, current) {
			return ErrStaleCell
		}
		return nil
	}
	return ErrNotFound
}

func (s *MemoryStore) CheckReleaseTarget(_ context.Context, owner, slug, releaseID, region string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.release(owner, slug, releaseID)
	if err != nil {
		return err
	}
	return s.checkReleaseTarget(r, region)
}

func (s *MemoryStore) RecordReleaseResult(_ context.Context, owner, slug, releaseID, region string, result ReleaseResult) error {
	if !result.valid() {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.release(owner, slug, releaseID)
	if err != nil {
		return err
	}
	if r.Generation != r.DesiredGeneration {
		return ErrSuperseded
	}
	if result.Outcome != ReleaseBlocked {
		if err := s.checkReleaseTarget(r, region); err != nil {
			return err
		}
	}
	for i := range r.Targets {
		target := &r.Targets[i]
		if target.Region != region {
			continue
		}
		if target.Outcome != ReleasePending {
			return nil
		} // terminal results cannot regress
		target.Outcome, target.Diagnostic, target.DeploymentID, target.UpdatedAt = result.Outcome, result.Diagnostic, result.DeploymentID, time.Now().UTC()
		s.releases[r.GlobalAppID][releaseID] = copyRelease(r)
		return nil
	}
	return ErrNotFound
}
