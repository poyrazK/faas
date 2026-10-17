package state

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (t *pgFinancialComputeTxn) jobAttempt(instance string) (financialComputeJobAttempt, error) {
	id, err := parsePgUUID(instance)
	if err != nil {
		return financialComputeJobAttempt{}, ErrInvalidArgument
	}
	a, err := t.q.FinancialComputeJobAttemptGet(t.ctx, t.db, id)
	if err != nil {
		return financialComputeJobAttempt{}, mapErr(err)
	}
	return financialComputeJobAttempt{AccountID: pgUUIDString(a.AccountID), JobID: pgUUIDString(a.JobID),
		RunID: pgUUIDString(a.RunID), TaskIndex: int(a.TaskIndex), InstanceID: pgUUIDString(a.InstanceID),
		NodeID: pgUUIDString(a.NodeID), Incarnation: pgUUIDString(a.Incarnation), RAMMB: int(a.RamMb), LeaseToken: pgUUIDString(a.LeaseToken)}, nil
}

func (t *pgFinancialComputeTxn) jobCurrent(l financial.ComputeLease) (bool, error) {
	id, _ := parsePgUUID(l.InstanceID)
	return t.q.FinancialComputeJobAttemptCurrent(t.ctx, t.db, sqlc.FinancialComputeJobAttemptCurrentParams{InstanceID: id, AccountID: t.account})
}

func (t *pgFinancialComputeTxn) jobBook(job string, month time.Time) (financial.ComputeBook, error) {
	id, _ := parsePgUUID(job)
	raw, err := t.q.FinancialComputeJobBookGet(t.ctx, t.db, sqlc.FinancialComputeJobBookGetParams{AccountID: t.account, JobID: id, PeriodStart: pgTime(month)})
	var b financial.ComputeBook
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &b)
	}
	return b, err
}

func (t *pgFinancialComputeTxn) putJobBook(b financial.ComputeBook) error {
	id, _ := parsePgUUID(b.JobID)
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return t.q.FinancialComputeJobBookPut(t.ctx, t.db, sqlc.FinancialComputeJobBookPutParams{AccountID: t.account, JobID: id, PeriodStart: pgTime(b.PeriodStart), PeriodEnd: pgTime(b.PeriodEnd), Payload: raw})
}

func (t *pgFinancialComputeTxn) jobReady(job string) (bool, error) {
	owners, err := t.q.FinancialComputeOwnersReady(t.ctx, t.db, 3*api.FinancialComputeOwnerHeartbeat.Seconds())
	if err != nil || !owners.Valid || !owners.Bool {
		return false, err
	}
	id, _ := parsePgUUID(job)
	safe, err := t.q.FinancialComputeJobActivationSafe(t.ctx, t.db, sqlc.FinancialComputeJobActivationSafeParams{AccountID: t.account, JobID: id})
	if err != nil || !safe.Valid || !safe.Bool {
		return false, err
	}
	ready, err := t.q.FinancialComputeJobSamplingReady(t.ctx, t.db, sqlc.FinancialComputeJobSamplingReadyParams{AccountID: t.account, JobID: id, PeriodStart: pgTime(financial.ComputeMonth(t.now))})
	return ready.Valid && ready.Bool, err
}

func (t *pgFinancialComputeTxn) initializeJob(job string) error {
	ready, err := t.jobReady(job)
	if err != nil {
		return err
	}
	if !ready {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.jobBook(job, month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: pgUUIDString(t.account), JobID: job, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	baseline, err := t.q.FinancialComputeJobBaseline(t.ctx, t.db, sqlc.FinancialComputeJobBaselineParams{AccountID: t.account, JobID: job, PeriodStart: pgTime(month), PeriodEnd: pgTime(b.PeriodEnd)})
	if err != nil {
		return err
	}
	if baseline.Incomplete {
		return financial.ErrComputeUnavailable
	}
	total, err := strconv.ParseInt(baseline.Amount, 10, 64)
	if err != nil || total < 0 || total > api.FinancialBudgetMoneyMax {
		return financial.ErrOverflow
	}
	b.Committed = max(b.Committed, total)
	return t.putJobBook(b)
}

func (s *PgStore) PrepareFinancialJobCompute(ctx context.Context, account, job string) error {
	return s.computeTransaction(ctx, account, func(t *pgFinancialComputeTxn) error {
		return prepareJobCompute(t, job, func() error { return t.initializeJob(job) }, t.ensureAccount)
	})
}

func computePgWorkloadIDs(key string) (app, job pgtype.UUID) {
	if id, ok := computeJobKey(key); ok {
		job, _ = parsePgUUID(id)
	} else {
		app, _ = parsePgUUID(key)
	}
	return app, job
}

func (s *PgStore) DeferFinancialJobClaim(ctx context.Context, run string, index int, instance, token string, next time.Time) error {
	a, err := (&pgFinancialComputeTxn{ctx: ctx, db: s.pool, q: sqlc.New()}).jobAttempt(instance)
	if err != nil {
		return err
	}
	if a.RunID != run || a.TaskIndex != index || a.LeaseToken != token {
		return financial.ErrComputeFence
	}
	return s.computeTransaction(ctx, a.AccountID, func(t *pgFinancialComputeTxn) error {
		id, _ := parsePgUUID(instance)
		runID, _ := parsePgUUID(run)
		leaseToken, _ := parsePgUUID(token)
		n, err := t.q.FinancialComputeJobClaimDefer(ctx, t.db, sqlc.FinancialComputeJobClaimDeferParams{RunID: runID, TaskIndex: int32(index), InstanceID: id, LeaseToken: leaseToken, NextAttemptAt: pgTime(next)})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
		return nil
	})
}
