package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrRuntimeGenerationMoved refuses an admission decided from an app's
// runtime generation that has since moved (ADR-1741). Nothing was written;
// the caller rereads and decides again.
var ErrRuntimeGenerationMoved = fmt.Errorf("%w: app runtime generation moved", ErrConflict)

type runtimeGenerationGuardKey struct{}

type runtimeGenerationGuard struct {
	appID      string
	generation int64
}

// WithRuntimeGenerationGuard makes instance admission inserts made with ctx
// require appID's runtime generation to still be generation (ADR-1741): a
// schedd that decided from its owner cache commits only if nothing the
// decision read has changed since.
func WithRuntimeGenerationGuard(ctx context.Context, appID string, generation int64) context.Context {
	return context.WithValue(ctx, runtimeGenerationGuardKey{}, runtimeGenerationGuard{appID: appID, generation: generation})
}

func runtimeGenerationGuardFrom(ctx context.Context) (runtimeGenerationGuard, bool) {
	guard, ok := ctx.Value(runtimeGenerationGuardKey{}).(runtimeGenerationGuard)
	return guard, ok
}

// RuntimeGenerationGuarded reports whether ctx carries appID's generation
// guard, so its writes may rely on the generation instead of rereading.
func RuntimeGenerationGuarded(ctx context.Context, appID string) bool {
	guard, ok := runtimeGenerationGuardFrom(ctx)
	return ok && guard.appID == appID
}

// generationGuardedPublication reports whether p publishes on the guard
// ctx carries: it asked to, carries no standards receipt, and ctx guards its
// app. A request without the guard is a caller error.
func generationGuardedPublication(ctx context.Context, p RuntimeInstancePublication) (runtimeGenerationGuard, bool, error) {
	if !p.GenerationGuarded {
		return runtimeGenerationGuard{}, false, nil
	}
	guard, ok := runtimeGenerationGuardFrom(ctx)
	if !ok || guard.appID != p.AppID || p.AdmissionReceipt != nil || p.PromotionReceipt != nil {
		return runtimeGenerationGuard{}, false, ErrInvalidArgument
	}
	return guard, true, nil
}

// CheckRuntimeGenerationGuard rereads the generation ctx's guard carries and
// refuses with ErrRuntimeGenerationMoved when it moved; without a guard it
// reads nothing. A write that does not carry the guard checks it this way
// before it acts, which leaves its decision as fresh as the reads the owner
// cache replaced.
func CheckRuntimeGenerationGuard(ctx context.Context, generations AppRuntimeGenerationReader) error {
	guard, ok := runtimeGenerationGuardFrom(ctx)
	if !ok {
		return nil
	}
	generation, err := generations.AppRuntimeGeneration(ctx, guard.appID)
	if errors.Is(err, ErrNotFound) || (err == nil && generation != guard.generation) {
		return ErrRuntimeGenerationMoved
	}
	if err != nil {
		return fmt.Errorf("state: read app runtime generation: %w", err)
	}
	return nil
}

// checkRuntimeGenerationTx share-locks the app's generation row inside the
// admission transaction, after the insert has taken its own locks: writers
// bump the generation after their row locks, so taking it last cannot close
// a lock cycle with them.
func checkRuntimeGenerationTx(ctx context.Context, tx pgx.Tx, guard runtimeGenerationGuard) error {
	generation, err := sqlc.New().LockAppRuntimeGeneration(ctx, tx, mustPgUUID(guard.appID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("state: read app runtime generation: %w", err)
	}
	if err != nil || generation != guard.generation {
		return ErrRuntimeGenerationMoved
	}
	return nil
}

// checkRuntimeGenerationLocked is the MemStore mirror; the caller holds m.mu.
func (m *MemStore) checkRuntimeGenerationLocked(ctx context.Context) error {
	guard, ok := runtimeGenerationGuardFrom(ctx)
	if !ok {
		return nil
	}
	generation, err := m.appRuntimeGenerationLocked(guard.appID)
	if err != nil || generation != guard.generation {
		return ErrRuntimeGenerationMoved
	}
	return nil
}
