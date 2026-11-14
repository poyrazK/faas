package object

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// overCeilingModeHost is the optional Host method that reads apid's
// `object_storage_over_ceiling_deletion` runtime config.
type overCeilingModeHost interface {
	ObjectStorageOverCeilingMode() string
}

// ObjectStorageOverCeilingMode is the rollout mode of over-ceiling deletion
// (ADR-20261111123000 decision 9), off when the host does not say.
func (s *Service) ObjectStorageOverCeilingMode() string {
	if h, ok := s.Host.(overCeilingModeHost); ok {
		if mode := h.ObjectStorageOverCeilingMode(); api.ValidObjectStorageOverCeilingMode(mode) {
			return mode
		}
	}
	return api.DefaultObjectStorageOverCeiling
}

// OverCeilingSweep counts one sweep: subjects over their ceiling, clocks it
// started and cleared, subjects past their deletion date, and subjects
// skipped because their usage was not fresh.
type OverCeilingSweep struct {
	Over, Started, Cleared, Due, Stale int
}

// SweepObjectStorageOverCeiling starts, keeps and clears each billing
// subject's over-ceiling clock (ADR-20261111123000 decisions 1 and 2). A
// subject is over when the bytes it stores, retained versions included,
// exceed its ceiling: the plan's or a support grant, tightened by its spend
// cap. Usage that is not fresh neither starts nor clears a clock. In shadow
// mode the clocks are recorded and logged with no customer effect; this
// sweep deletes nothing in any mode yet.
func (s *Service) SweepObjectStorageOverCeiling(ctx context.Context, now time.Time) (OverCeilingSweep, error) {
	var out OverCeilingSweep
	mode := s.ObjectStorageOverCeilingMode()
	registry := s.Host.ObjectStorage()
	store := s.Host.Store()
	usage, ok := store.(state.ObjectStorageAccountingStore)
	clocks, ok2 := store.(state.ObjectStorageOverCeilingStore)
	if mode == api.ObjectStorageOverCeilingOff || registry == nil || !ok || !ok2 {
		return out, nil
	}
	accounts, err := state.ListBillingResourceAccounts(ctx, store)
	if err != nil {
		return out, err
	}
	now = now.UTC()
	seen := map[string]bool{}
	var failures []error
	for _, acct := range accounts {
		subject := acct.ResourceOwnerID()
		if seen[subject] {
			continue
		}
		seen[subject] = true
		snapshot, err := usage.ObjectUsage(ctx, subject, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		summary := state.SummarizeObjectUsage(snapshot, registry.Accounting, now)
		if !summary.Fresh {
			out.Stale++
			continue
		}
		failures = append(failures, s.observeOverCeiling(ctx, clocks, subject, summary.CapacityBytes, snapshot.PlanLimits.MaxAccountBytes, mode, now, &out))
	}
	return out, errors.Join(failures...)
}

func (s *Service) observeOverCeiling(ctx context.Context, clocks state.ObjectStorageOverCeilingStore, subject string, stored, ceiling int64, mode string, now time.Time, out *OverCeilingSweep) error {
	log := s.Host.Logger().With("subject_id", subject, "stored_bytes", stored, "ceiling_bytes", ceiling, "mode", mode)
	if ceiling <= 0 || stored <= ceiling {
		cleared, err := clocks.ClearObjectStorageOverCeiling(ctx, subject)
		if cleared {
			out.Cleared++
			log.Info("object storage back within ceiling; deletion clock cleared")
		}
		return err
	}
	c, err := clocks.ObserveObjectStorageOverCeiling(ctx, subject, stored, ceiling, now)
	if err != nil {
		return err
	}
	out.Over++
	log = log.With("over_ceiling_since", c.OverCeilingSince, "deletion_scheduled_at", c.DeletionScheduledAt, "excess_bytes", stored-ceiling)
	if c.Started(now) {
		out.Started++
		log.Info("object storage over ceiling; deletion clock started")
	}
	if !now.Before(c.DeletionScheduledAt) {
		out.Due++
		log.Warn("object storage over-ceiling deletion due; nothing deleted in this mode")
	}
	return nil
}

// OverCeilingDeletion is what the usage API shows of a subject's clock, nil
// when the subject fits or customers do not see the clock yet.
func (s *Service) OverCeilingDeletion(ctx context.Context, subject string) (*api.ObjectStorageOverCeilingDeletion, error) {
	clocks, ok := s.Host.Store().(state.ObjectStorageOverCeilingStore)
	if !ok || !api.ObjectStorageOverCeilingVisible(s.ObjectStorageOverCeilingMode()) {
		return nil, nil
	}
	c, err := clocks.GetObjectStorageOverCeiling(ctx, subject)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.ObjectStorageOverCeilingDeletion{OverCeilingSince: c.OverCeilingSince, DeletionScheduledAt: c.DeletionScheduledAt, ExcessBytes: c.StoredBytes - c.CeilingBytes}, nil
}
