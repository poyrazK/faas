package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ RuntimeUpgradeVerificationJournalStore = (*MemStore)(nil)

func (m *MemStore) StartRuntimeUpgradeVerification(ctx context.Context, accountID, id string, sessions []string) (RuntimeUpgradeVerificationJournal, error) {
	out, err := newRuntimeUpgradeVerification(id, sessions, time.Now().UTC())
	if err != nil || validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeVerificationJournal{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeVerificationJournal{}, err
	}
	op, ok := m.runtimeUpgradeOperations[id]
	if !ok || op.AccountID != accountID {
		return RuntimeUpgradeVerificationJournal{}, ErrNotFound
	}
	if old, ok := m.runtimeUpgradeVerifications[id]; ok {
		if !slices.Equal(old.GatewaySessions, out.GatewaySessions) {
			return RuntimeUpgradeVerificationJournal{}, ErrConflict
		}
		return cloneRuntimeUpgradeVerificationJournal(old), nil
	}
	cutover, ok := m.runtimeUpgradeCutovers[op.DeploymentID]
	if !ok || op.Phase != RuntimeUpgradeComplete || !op.cutover(op.WakeID).matches(cutover) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	j := RuntimeUpgradeVerificationJournal{OperationID: id, GatewaySessions: out.GatewaySessions, Phase: RuntimeUpgradeVerificationPending,
		CutoverAt: cutover.CutoverAt, CreatedAt: out.CheckedAt, NextAttemptAt: out.CheckedAt, DeadlineAt: cutover.CutoverAt.Add(api.RuntimeUpgradeVerificationMaxAge)}
	if m.runtimeUpgradeVerifications == nil {
		m.runtimeUpgradeVerifications = map[string]RuntimeUpgradeVerificationJournal{}
	}
	m.runtimeUpgradeVerifications[id] = j
	return cloneRuntimeUpgradeVerificationJournal(j), nil
}

func (m *MemStore) RuntimeUpgradeVerificationJournal(_ context.Context, accountID, id string) (RuntimeUpgradeVerificationJournal, error) {
	if validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeVerificationJournal{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.runtimeUpgradeVerifications[id]
	if !ok || m.runtimeUpgradeOperations[id].AccountID != accountID {
		return RuntimeUpgradeVerificationJournal{}, ErrNotFound
	}
	return cloneRuntimeUpgradeVerificationJournal(j), nil
}

func (m *MemStore) ClaimRuntimeUpgradeVerification(ctx context.Context) (RuntimeUpgradeOperationClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeOperationClaim{}, err
	}
	now := time.Now().UTC()
	var due []RuntimeUpgradeVerificationJournal
	for _, j := range m.runtimeUpgradeVerifications {
		if j.Phase == RuntimeUpgradeVerificationPending && (!j.NextAttemptAt.After(now) || !j.DeadlineAt.After(now)) && !j.LeaseUntil.After(now) {
			due = append(due, j)
		}
	}
	if len(due) == 0 {
		return RuntimeUpgradeOperationClaim{}, ErrNotFound
	}
	sort.Slice(due, func(i, k int) bool {
		if !due[i].NextAttemptAt.Equal(due[k].NextAttemptAt) {
			return due[i].NextAttemptAt.Before(due[k].NextAttemptAt)
		}
		if !due[i].CreatedAt.Equal(due[k].CreatedAt) {
			return due[i].CreatedAt.Before(due[k].CreatedAt)
		}
		return due[i].OperationID < due[k].OperationID
	})
	j := due[0]
	j.LeaseToken, j.LeaseUntil = uuid.NewString(), now.Add(api.RuntimeUpgradeOperationLease)
	m.runtimeUpgradeVerifications[j.OperationID] = j
	return RuntimeUpgradeOperationClaim{ID: j.OperationID, LeaseToken: j.LeaseToken}, nil
}

func (m *MemStore) AdvanceRuntimeUpgradeVerification(ctx context.Context, claim RuntimeUpgradeOperationClaim) (RuntimeUpgradeVerificationJournal, error) {
	if err := claim.validate(); err != nil {
		return RuntimeUpgradeVerificationJournal{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeVerificationJournal{}, err
	}
	now := time.Now().UTC()
	j, ok := m.runtimeUpgradeVerifications[claim.ID]
	if !ok || j.Phase != RuntimeUpgradeVerificationPending || j.LeaseToken != claim.LeaseToken || !j.LeaseUntil.After(now) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	out, _ := newRuntimeUpgradeVerification(claim.ID, j.GatewaySessions, now)
	if !j.DeadlineAt.After(now) {
		out.Reason = "deadline_exceeded"
	} else {
		var err error
		out, err = m.verifyRuntimeUpgradeLocked(m.runtimeUpgradeOperations[claim.ID].AccountID, out)
		if err != nil {
			return RuntimeUpgradeVerificationJournal{}, err
		}
	}
	now = time.Now().UTC()
	if !j.LeaseUntil.After(now) || (out.Status == "verified" && !out.CheckedAt.Add(time.Duration(out.ValidForSeconds)*time.Second).After(now)) {
		return RuntimeUpgradeVerificationJournal{}, ErrConflict
	}
	j = runtimeUpgradeVerificationCheckpoint(j, out, now)
	j.NextAttemptAt = now.Add(api.RuntimeUpgradeOperationInterval)
	m.runtimeUpgradeVerifications[claim.ID] = j
	return cloneRuntimeUpgradeVerificationJournal(j), nil
}
