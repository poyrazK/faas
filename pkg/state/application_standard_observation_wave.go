package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) revalidateStandardWaveLocked(ctx context.Context, o *ApplicationStandardOperation, index int) (time.Time, bool, error) {
	start := index / o.BatchSize * o.BatchSize
	if start == 0 {
		return time.Time{}, true, nil
	}
	qualified := map[string]standardApplicationQualification{}
	deadline := standardApplicationQualification{}
	ready := true
	for _, target := range o.Targets[:start] {
		if target.State != "observed" {
			continue
		}
		q, err := m.qualifyStandardApplicationLocked(ctx, o.OrgID, target.AppID, target.DesiredRevision)
		if err != nil {
			return time.Time{}, false, err
		}
		qualified[target.AppID] = q
		ready = ready && q.reason == "" && !standardObservationEvidenceExpired(q, time.Now())
		if q.reason == "" {
			deadline.restrict(q.until)
		}
	}
	if !ready {
		m.checkpointStandardObservationsLocked(o, qualified, time.Now().UTC())
	}
	return deadline.until, ready, nil
}

func revalidateStandardWaveTx(ctx context.Context, tx pgx.Tx, c ApplicationStandardWorkerClaim, o *ApplicationStandardOperation, index int) (bool, error) {
	start := index / o.BatchSize * o.BatchSize
	if start == 0 {
		return true, nil
	}
	qualified := map[string]standardApplicationQualification{}
	ready := true
	for _, target := range o.Targets[:start] {
		if target.State != "observed" {
			continue
		}
		q, err := qualifyStandardApplicationTx(ctx, tx, o.OrgID, target.AppID, target.DesiredRevision)
		if err != nil {
			return false, err
		}
		qualified[target.AppID] = q
		ready = ready && q.reason == "" && !q.until.IsZero()
	}
	if !ready {
		return false, checkpointStandardObservationsTx(ctx, tx, c, o, qualified)
	}
	if len(qualified) == 0 {
		return true, nil
	}
	return true, restrictStandardObservationClaimTx(ctx, tx, c, qualified)
}

func restrictStandardObservationClaimTx(ctx context.Context, tx pgx.Tx, c ApplicationStandardWorkerClaim, qualified map[string]standardApplicationQualification) error {
	deadline := standardApplicationQualification{}
	for _, q := range qualified {
		if q.reason == "" && !q.until.IsZero() {
			deadline.restrict(q.until)
		}
	}
	if deadline.until.IsZero() {
		return nil
	}
	count, err := sqlc.New().RestrictApplicationStandardWorkerEvidence(ctx, tx, sqlc.RestrictApplicationStandardWorkerEvidenceParams{OperationID: mustPgUUID(c.OperationID), OrgID: mustPgUUID(c.OrgID), Owner: c.Owner, Generation: c.Generation, EvidenceUntil: standardPgTime(deadline.until)})
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrApplicationStandardLeaseLost
	}
	return nil
}
