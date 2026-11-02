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
// ceiling (ADR-20261101214152). The ceiling uses the spend cap the subject
// would hold afterwards (ADR-20261029180000), since a default cap follows the
// plan. Task is empty when the bytes fit, or when no object storage or no
// ceiling applies.
func (s *Service) PlanChangeStorage(ctx context.Context, subject state.Account, target api.Plan) (PlanChangeStorage, error) {
	registry := s.Host.ObjectStorage()
	st, ok := s.Host.Store().(state.ObjectStorageAccountingStore)
	limits, known := api.LimitsFor(target)
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
	ceiling := limits.ObjectStorage.WithSpendCap(state.PlanChangeOverageCap(subject.Plan, target, current)).MaxAccountBytes
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
	if target.IsPaid() {
		out.Task += ", or raise your spend cap"
	}
	return out, nil
}

// gibAtLeast rounds bytes up to whole GiB, so a task never asks for too little.
func gibAtLeast(bytes int64) string {
	return fmt.Sprintf("%d GiB", (bytes+(1<<30)-1)>>30)
}

// gibAtMost rounds bytes down to whole GiB.
func gibAtMost(bytes int64) string {
	return fmt.Sprintf("%d GiB", bytes>>30)
}
