package object

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// PlanChangeStorage is what a plan change would leave of a subject's object
// storage: the bytes it stores and the target plan's ceiling with the spend
// cap the subject holds after the change.
type PlanChangeStorage struct {
	StoredBytes, CeilingBytes int64
	Task                      string
}

// PlanChangeStorage checks a downgrade against the target plan's storage
// ceiling (ADR-20261101214152): the plan's, or the one support granted the
// subject, tightened by the spend cap the subject would hold afterwards
// (ADR-20261029180000), since a default cap follows the plan. Task is empty
// when the bytes fit, or when no object storage or no ceiling applies.
func (s *Service) PlanChangeStorage(ctx context.Context, subject state.Account, target api.Plan) (PlanChangeStorage, error) {
	registry := s.Host.ObjectStorage()
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	_, known := api.LimitsFor(target)
	if registry == nil || !ok || !known {
		return PlanChangeStorage{}, nil
	}
	owner := subject.ResourceOwnerID()
	var current *int64
	if cents, capped, err := s.Host.Store().GetAccountOverageCapCents(ctx, owner); err != nil {
		return PlanChangeStorage{}, fmt.Errorf("read spend cap: %w", err)
	} else if capped {
		current = &cents
	}
	grant, err := objectStorageCeilingGrant(ctx, s.Host.Store(), owner)
	if err != nil {
		return PlanChangeStorage{}, err
	}
	planCeiling := api.ObjectStorageLimitsFor(target, grant, nil).MaxAccountBytes
	ceiling := api.ObjectStorageLimitsFor(target, grant, state.PlanChangeOverageCap(subject.Plan, target, current)).MaxAccountBytes
	if ceiling <= 0 {
		return PlanChangeStorage{}, nil
	}
	now := time.Now().UTC()
	snapshot, err := st.ObjectUsage(ctx, owner, now)
	if err != nil {
		return PlanChangeStorage{}, fmt.Errorf("read object storage usage: %w", err)
	}
	out := PlanChangeStorage{StoredBytes: state.SummarizeObjectUsage(snapshot, registry.Accounting, now).CapacityBytes, CeilingBytes: ceiling}
	if out.StoredBytes <= ceiling {
		return out, nil
	}
	out.Task = fmt.Sprintf("delete %s of object storage: the %s plan stores at most %s and you store %s", gibAtLeast(out.StoredBytes-ceiling), target, gibAtMost(ceiling), gibAtLeast(out.StoredBytes))
	switch {
	case target.IsPaid() && ceiling < planCeiling:
		out.Task += ", or raise your spend cap"
	case target.IsPaid():
		out.Task += ", or ask support for a higher storage ceiling"
	}
	return out, nil
}

// objectStorageCeilingGrant returns the ceiling support granted owner, nil
// when it has none or the store keeps no grants.
func objectStorageCeilingGrant(ctx context.Context, store state.Store, owner string) (*int64, error) {
	grants, ok := store.(state.ObjectStorageCeilingGrantStore)
	if !ok {
		return nil, nil
	}
	grant, err := grants.ObjectStorageCeilingGrant(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("read storage ceiling grant: %w", err)
	}
	if grant == nil {
		return nil, nil
	}
	return &grant.MaxBytes, nil
}

// gibAtLeast rounds bytes up to whole GiB, so a task never asks for too little.
func gibAtLeast(bytes int64) string {
	return fmt.Sprintf("%d GiB", (bytes+(1<<30)-1)>>30)
}

// gibAtMost rounds bytes down to whole GiB.
func gibAtMost(bytes int64) string {
	return fmt.Sprintf("%d GiB", bytes>>30)
}
