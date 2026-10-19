package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

type dependencyBackend interface {
	CutPostgres(context.Context, rehearsalpg.Resource, func(context.Context) error) (int64, error)
	RecoverPostgres(context.Context, rehearsalpg.Resource) error
}

// Recovery is independent of the new-admission flag. Restarted owners may clean
// expired obligations, but cannot adopt the old collector's successful proof.
func (s *Service) reconcilePostgresFaults(ctx context.Context, shutdown bool) {
	c, ok := s.Catalog.(rehearsalfault.Catalog)
	if !ok {
		return
	}
	rows, err := c.PendingScenarioPostgresFaults(ctx)
	if err != nil {
		return
	}
	s.faultMu.Lock()
	defer s.faultMu.Unlock()
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		if shutdown && row.OwnerID != s.OwnerID {
			continue
		}
		current, err := c.ScenarioPostgresFault(ctx, row.ID)
		if err != nil {
			continue
		}
		s.stepPostgresFault(ctx, c, current, shutdown)
	}
}

func (s *Service) stepPostgresFault(ctx context.Context, c rehearsalfault.Catalog, f rehearsalfault.Row, force bool) {
	b, ok := s.Backend.(dependencyBackend)
	if !ok {
		return
	}
	r, err := s.Catalog.GetScenarioPostgres(ctx, f.AccountID, f.RunID)
	if err != nil || r.ID != f.ResourceID || r.BackendSHA256 != f.BackendSHA256 || s.Backend.Identity() != f.BackendSHA256 {
		return
	}
	if f.State == "recovered" || f.State == "failed" && f.Recovered {
		return
	}
	if f.OwnerID != s.OwnerID && time.Now().Before(f.DeadlineAt) {
		return
	}
	if force || f.OwnerID != s.OwnerID || r.Status != "ready" || !r.ExpiresAt.After(time.Now()) {
		reason := "fault owner or fixture lifetime was lost"
		if force {
			reason = "fault collector stopped"
		}
		s.recoverPostgresFault(ctx, c, b, r, f, reason)
		return
	}
	switch f.State {
	case "requested":
		if time.Now().After(f.DeadlineAt) {
			s.recoverPostgresFault(ctx, c, b, r, f, "checkpoint activation expired")
			return
		}
		f.State = "cutting"
		if c.TransitionScenarioPostgresFault(ctx, f, "requested") != nil {
			return
		}
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		closed, err := b.CutPostgres(call, r, func(check context.Context) error {
			// Check after taking the physical database lock. A replacement may
			// already have restored an abandoned cutting request while this
			// owner was delayed; it must never apply that stale cut afterward.
			current, err := c.ScenarioPostgresFault(check, f.ID)
			if err != nil || current.State != "cutting" || current.OwnerID != s.OwnerID || current.Error != "" || !current.DeadlineAt.After(time.Now()) {
				return ErrFixture
			}
			resource, err := s.Catalog.GetScenarioPostgres(check, f.AccountID, f.RunID)
			if err != nil || resource.ID != f.ResourceID || resource.OwnerID != f.OwnerID || resource.BackendSHA256 != f.BackendSHA256 || resource.Status != "ready" || !resource.ExpiresAt.After(time.Now()) {
				return ErrFixture
			}
			return nil
		})
		cancel()
		if err != nil {
			s.recoverPostgresFault(context.WithoutCancel(ctx), c, b, r, f, "database disconnect was not confirmed")
			return
		}
		f.State, f.Activated, f.ClosedConnections = "active", true, closed
		f.RestoreAt = time.Now().UTC().Add(time.Duration(f.Spec.UnavailableMS) * time.Millisecond)
		deadline := f.RestoreAt.Add(time.Duration(max(f.Spec.RecoverWithinMS, 10000))*time.Millisecond + 10*time.Second)
		if deadline.After(f.DeadlineAt) {
			f.DeadlineAt = deadline
		}
		if c.TransitionScenarioPostgresFault(ctx, f, "cutting") != nil {
			current, err := c.ScenarioPostgresFault(context.WithoutCancel(ctx), f.ID)
			if err == nil {
				s.recoverPostgresFault(context.WithoutCancel(ctx), c, b, r, current, "activation receipt persistence was lost")
			}
		}
	case "active":
		if !time.Now().Before(f.RestoreAt) {
			s.recoverPostgresFault(ctx, c, b, r, f, "")
		}
	case "cutting":
		s.recoverPostgresFault(ctx, c, b, r, f, "database disconnect acknowledgement was lost")
	case "recovering", "failed":
		s.recoverPostgresFault(ctx, c, b, r, f, "recovery acknowledgement was lost")
	}
}

func (s *Service) recoverPostgresFault(ctx context.Context, c rehearsalfault.Catalog, b dependencyBackend, r rehearsalpg.Resource, f rehearsalfault.Row, reason string) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if f.Error == "" {
		f.Error = reason
	}
	if f.State != "recovering" {
		expected := f.State
		f.State = "recovering"
		if c.TransitionScenarioPostgresFault(call, f, expected) != nil {
			return
		}
	}
	if f.Activated && !f.RestoreAt.IsZero() {
		f.OutageMS = max(int64(0), time.Since(f.RestoreAt.Add(-time.Duration(f.Spec.UnavailableMS)*time.Millisecond)).Milliseconds())
	}
	if r.Status == "deleted" || r.Status == "complete" {
		f.Recovered = true
		if f.Error == "" {
			f.Error = "fixture was deleted before recovery observation"
		}
	} else if b.RecoverPostgres(call, r) == nil {
		f.Recovered = true
	} else if f.Error == "" {
		f.Error = "database recovery was not confirmed"
	}
	if !f.RestoreAt.IsZero() {
		f.RecoveryMS = max(int64(0), time.Since(f.RestoreAt).Milliseconds())
	}
	f.State = "recovered"
	if f.Error != "" || !f.Activated || !f.Recovered {
		f.State = "failed"
	}
	_ = c.TransitionScenarioPostgresFault(call, f, "recovering")
}

// Called under faultMu before ordinary fixture fencing. Deletion always tries
// to restore a retained outage first; a verified Drop is the final cleanup fence.
func (s *Service) settlePostgresFault(ctx context.Context, account, run string, deleting bool) error {
	c, ok := s.Catalog.(rehearsalfault.Catalog)
	if !ok {
		return nil
	}
	f, err := c.ScenarioPostgresFaultForRun(ctx, account, run)
	if err != nil {
		if errors.Is(err, rehearsalfault.ErrNotFound) {
			return nil
		}
		return ErrFixture
	}
	if !deleting {
		if f.Validate() != nil {
			return ErrFixture
		}
		return nil
	}
	if f.Recovered {
		return nil
	}
	b, ok := s.Backend.(dependencyBackend)
	if !ok {
		return ErrFixture
	}
	r, err := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if err != nil {
		return ErrFixture
	}
	if r.ID != f.ResourceID || r.BackendSHA256 != f.BackendSHA256 || s.Backend.Identity() != f.BackendSHA256 {
		return ErrFixture
	}
	s.recoverPostgresFault(ctx, c, b, r, f, "fixture cleanup interrupted the fault")
	return nil
}
