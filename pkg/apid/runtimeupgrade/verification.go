package runtimeupgrade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// StartVerification fixes reviewed identities after activation. No route or
// fleet discovery is registered. Retries must submit the same canonical set.
func (c Controls) StartVerification(ctx context.Context, accountID, id string, sessions []string) (state.RuntimeUpgradeVerificationJournal, error) {
	store, ok := c.Store.(state.RuntimeUpgradeVerificationJournalStore)
	if !ok {
		return state.RuntimeUpgradeVerificationJournal{}, state.ErrInvalidArgument
	}
	j, err := store.StartRuntimeUpgradeVerification(ctx, accountID, id, sessions)
	if err != nil {
		return state.RuntimeUpgradeVerificationJournal{}, fmt.Errorf("start runtime verification: %w", err)
	}
	return safeVerificationJournal(j), nil
}

// VerificationStatus returns historical progress. Use Verify for current proof.
func (c Controls) VerificationStatus(ctx context.Context, accountID, id string) (state.RuntimeUpgradeVerificationJournal, error) {
	store, ok := c.Store.(state.RuntimeUpgradeVerificationJournalStore)
	if !ok {
		return state.RuntimeUpgradeVerificationJournal{}, state.ErrInvalidArgument
	}
	j, err := store.RuntimeUpgradeVerificationJournal(ctx, accountID, id)
	if err != nil {
		return state.RuntimeUpgradeVerificationJournal{}, fmt.Errorf("read runtime verification progress: %w", err)
	}
	return safeVerificationJournal(j), nil
}

func safeVerificationJournal(j state.RuntimeUpgradeVerificationJournal) state.RuntimeUpgradeVerificationJournal {
	j.LeaseToken = ""
	j.LeaseUntil, j.NextAttemptAt = time.Time{}, time.Time{}
	return j
}

// VerificationExecutor consumes only enrolled verification work. A fresh lease
// after restart evaluates fresh evidence; no observation survives in memory.
type VerificationExecutor struct {
	Store state.RuntimeUpgradeVerificationJournalStore
}

func (e VerificationExecutor) RunOnce(ctx context.Context) (bool, error) {
	if e.Store == nil {
		return false, errors.New("runtime verification executor: store unavailable")
	}
	stepCtx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeOperationLease)
	defer cancel()
	claim, err := e.Store.ClaimRuntimeUpgradeVerification(stepCtx)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("runtime verification executor: claim: %w", err)
	}
	if _, err := e.Store.AdvanceRuntimeUpgradeVerification(stepCtx, claim); err != nil {
		return true, fmt.Errorf("runtime verification executor: advance: %w", err)
	}
	return true, nil
}
