package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const overageRecoveryPageSize = 100

// A reserved step may already have reached the provider. The new-usage close
// window and effective-from setting cannot abandon its immutable receipt.
func (b *OverageBiller) recoverPending(ctx context.Context, through time.Time, summary *OverageBillingSummary) (map[OveragePendingRef]bool, error) {
	handled := make(map[OveragePendingRef]bool)
	var after OveragePendingRef
	var failures []error
	for {
		refs, err := b.billing.PendingOverage(ctx, b.options.Provider, after, through, overageRecoveryPageSize)
		if err != nil {
			return handled, errors.Join(append(failures, err)...)
		}
		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return handled, errors.Join(append(failures, err)...)
			}
			handled[ref] = true
			summary.Subjects++
			if err := b.recoverStep(ctx, ref, summary); err != nil {
				summary.Failed++
				failures = append(failures, fmt.Errorf("pending subject %s %s: %w", ref.SubjectID, ref.PeriodStart.Format("2006-01"), err))
			}
		}
		if len(refs) < overageRecoveryPageSize {
			return handled, errors.Join(failures...)
		}
		after = refs[len(refs)-1]
	}
}

func (b *OverageBiller) recoverStep(ctx context.Context, ref OveragePendingRef, summary *OverageBillingSummary) error {
	pending, found, err := b.billing.OveragePending(ctx, b.options.Provider, ref.SubjectID, ref.PeriodStart)
	if err != nil || !found {
		return err // A replica may have completed it since discovery.
	}
	if _, err := b.options.Allowance(ctx, ref.SubjectID, ref.PeriodStart); errors.Is(err, ErrNotFound) {
		return nil // A deleted subject has no billing customer left to charge.
	}
	return b.send(ctx, pending, summary)
}
