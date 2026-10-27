// adr: 566 — reservations, policy mutation and settlement share an account lock.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ FinancialComputeStore = (*PgStore)(nil)

func (s *PgStore) CancelFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	reader := &pgFinancialComputeTxn{ctx: ctx, db: s.pool, q: sqlc.New()}
	a, readErr := reader.jobAttempt(r.InstanceID)
	if readErr == nil {
		if a.NodeID != r.NodeID {
			return out, financial.ErrComputeFence
		}
		r = a.request(r.Incarnation)
	} else if r.AppID != "" && (errors.Is(readErr, ErrNotFound) || errors.Is(readErr, ErrInvalidArgument)) {
		app, readErr := s.AppByID(ctx, r.AppID)
		if readErr != nil {
			return out, readErr
		}
		r.AccountID = app.AccountID
	} else {
		return out, errors.Join(financial.ErrComputeFence, readErr)
	}
	err = s.computeTransaction(ctx, r.AccountID, func(tx *pgFinancialComputeTxn) error { var err error; out, err = computeCancel(tx, r); return err })
	return out, err
}

type pgFinancialComputeTxn struct {
	ctx     context.Context
	db      sqlc.DBTX
	q       *sqlc.Queries
	account pgtype.UUID
	now     time.Time
}

func (t *pgFinancialComputeTxn) clock() time.Time { return t.now }
func (t *pgFinancialComputeTxn) book(app string, month time.Time) (financial.ComputeBook, error) {
	if job, ok := computeJobKey(app); ok {
		return t.jobBook(job, month)
	}
	if scope, ok := computeSharedScopeOf(app); ok {
		return t.scopeBook(scope, month)
	}
	if app == "" {
		return t.accountBook(month)
	}
	id, _ := parsePgUUID(app)
	raw, err := t.q.FinancialComputeBookGet(t.ctx, t.db, sqlc.FinancialComputeBookGetParams{AccountID: t.account, AppID: id, PeriodStart: pgTime(month)})
	var b financial.ComputeBook
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &b)
	}
	return b, err
}
func (t *pgFinancialComputeTxn) putBook(b financial.ComputeBook) error {
	if b.ProjectID != "" || b.EnvironmentID != "" {
		return t.putScopeBook(b)
	}
	if b.JobID != "" {
		return t.putJobBook(b)
	}
	if b.AppID == "" {
		return t.putAccountBook(b)
	}
	id, _ := parsePgUUID(b.AppID)
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return t.q.FinancialComputeBookPut(t.ctx, t.db, sqlc.FinancialComputeBookPutParams{AccountID: t.account, AppID: id, PeriodStart: pgTime(b.PeriodStart), PeriodEnd: pgTime(b.PeriodEnd), Payload: raw})
}
func (t *pgFinancialComputeTxn) lease(id string) (financial.ComputeLease, error) {
	key, _ := parsePgUUID(id)
	raw, err := t.q.FinancialComputeLeaseGet(t.ctx, t.db, sqlc.FinancialComputeLeaseGetParams{AccountID: t.account, ID: key})
	var l financial.ComputeLease
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &l)
	}
	return l, err
}
func (t *pgFinancialComputeTxn) putLease(l financial.ComputeLease) error {
	id, _ := parsePgUUID(l.ID)
	app, _ := parsePgUUID(l.AppID)
	job, _ := parsePgUUID(l.JobID)
	run, _ := parsePgUUID(l.JobRunID)
	index := pgtype.Int4{Int32: int32(l.JobTaskIndex), Valid: l.JobID != ""}
	node, _ := parsePgUUID(l.NodeID)
	incarnation, _ := parsePgUUID(l.Incarnation)
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	var settled pgtype.Timestamptz
	if l.SettledAt != nil {
		settled = pgTime(*l.SettledAt)
	}
	count, err := t.q.FinancialComputeLeasePut(t.ctx, t.db, sqlc.FinancialComputeLeasePutParams{ID: id, AccountID: t.account, AppID: app, JobID: job, JobRunID: run, JobTaskIndex: index, InstanceID: l.InstanceID, NodeID: node, Incarnation: incarnation, PeriodStart: pgTime(l.PeriodStart), Strict: l.Strict, StartedAt: pgTime(l.StartedAt), FundedUntil: pgTime(l.FundedUntil), SettledAt: settled, Payload: raw})
	if err != nil {
		return err
	}
	if count != 1 {
		return financial.ErrComputeFence
	}
	return nil
}
func decodeComputeLeases(rows [][]byte, err error) ([]financial.ComputeLease, error) {
	if err != nil {
		return nil, err
	}
	if len(rows) > api.FinancialComputeActiveMax {
		return nil, financial.ErrComputeUnavailable
	}
	out := make([]financial.ComputeLease, 0, len(rows))
	for _, raw := range rows {
		var l financial.ComputeLease
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}
func (t *pgFinancialComputeTxn) active(app string) ([]financial.ComputeLease, error) {
	if scope, ok := computeSharedScopeOf(app); ok {
		return t.scopeLeases(scope)
	}
	if job, ok := computeJobKey(app); ok {
		id, _ := parsePgUUID(job)
		return decodeComputeLeases(t.q.FinancialComputeJobLeases(t.ctx, t.db, sqlc.FinancialComputeJobLeasesParams{AccountID: t.account, JobID: id}))
	}
	if app == "" {
		return decodeComputeLeases(t.q.FinancialComputeAccountLeases(t.ctx, t.db, t.account))
	}
	id, _ := parsePgUUID(app)
	return decodeComputeLeases(t.q.FinancialComputeAppLeases(t.ctx, t.db, sqlc.FinancialComputeAppLeasesParams{AccountID: t.account, AppID: id}))
}
func (t *pgFinancialComputeTxn) policies(app string) ([]FinancialBudget, error) {
	all, err := t.allPolicies()
	if err != nil {
		return nil, err
	}
	out := []FinancialBudget{}
	for _, p := range all {
		if computePolicyMatches(p, app) {
			out = append(out, p)
		}
	}
	return out, nil
}
func (t *pgFinancialComputeTxn) allPolicies() ([]FinancialBudget, error) {
	rows, err := t.q.FinancialBudgetList(t.ctx, t.db, sqlc.FinancialBudgetListParams{AccountID: t.account, PageSize: api.FinancialBudgetsPerAccount + 1})
	if err != nil {
		return nil, err
	}
	out := make([]FinancialBudget, 0, len(rows))
	for _, row := range rows {
		p, err := financialBudgetFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (t *pgFinancialComputeTxn) holds() ([]financialComputeHold, error) {
	rows, err := t.q.FinancialComputeHolds(t.ctx, t.db, t.account)
	if err != nil {
		return nil, err
	}
	out := make([]financialComputeHold, 0, len(rows))
	for _, h := range rows {
		out = append(out, financialComputeHold{pgUUIDString(h.PolicyID), h.Revision, h.PeriodStart.Time})
	}
	return out, nil
}
func (t *pgFinancialComputeTxn) putHold(h financialComputeHold) error {
	id, _ := parsePgUUID(h.PolicyID)
	return t.q.FinancialComputeHoldPut(t.ctx, t.db, sqlc.FinancialComputeHoldPutParams{AccountID: t.account, PolicyID: id, Revision: h.Revision, PeriodStart: pgTime(h.PeriodStart)})
}
func (t *pgFinancialComputeTxn) owner(node, incarnation string) (bool, error) {
	n, _ := parsePgUUID(node)
	i, _ := parsePgUUID(incarnation)
	return t.q.FinancialComputeOwnerValid(t.ctx, t.db, sqlc.FinancialComputeOwnerValidParams{NodeID: n, Incarnation: i})
}

func (s *PgStore) computeTransaction(ctx context.Context, account string, fn func(*pgFinancialComputeTxn) error) error {
	if uuid.Validate(account) != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, _ := parsePgUUID(account)
	q := sqlc.New()
	if _, err = q.FinancialBudgetAccountLock(ctx, tx, id); err != nil {
		return financialBudgetStoreError(err)
	}
	now, err := q.FinancialComputeClock(ctx, tx)
	if err != nil {
		return err
	}
	err = fn(&pgFinancialComputeTxn{ctx: ctx, db: tx, q: q, account: id, now: now.Time})
	// A denied reservation retains its durable hold, but no money mutation.
	if err != nil && !errors.Is(err, financial.ErrComputeBudget) {
		return err
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return commitErr
	}
	return err
}

func (s *PgStore) AcquireFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	err = s.computeTransaction(ctx, r.AccountID, func(tx *pgFinancialComputeTxn) error {
		if r.JobRunID != "" {
			a, err := tx.jobAttempt(r.InstanceID)
			if err != nil || !computeJobClaimMatches(a, r) {
				return errors.Join(financial.ErrComputeFence, err)
			}
			r.JobID = a.JobID
			valid, err := tx.jobCurrent(financial.ComputeLease{InstanceID: r.InstanceID})
			if err != nil || !valid {
				return errors.Join(financial.ErrComputeFence, err)
			}
		} else {
			app, _ := parsePgUUID(r.AppID)
			owned, err := tx.q.FinancialComputeAppOwned(ctx, tx.db, sqlc.FinancialComputeAppOwnedParams{AccountID: tx.account, AppID: app})
			if err != nil {
				return err
			}
			if !owned {
				return ErrNotFound
			}
		}
		scopes, policies, err := computeAttemptPolicies(tx, r.WorkloadKey(), r.InstanceID, r.DeploymentID)
		if err != nil {
			return err
		}
		if len(policies) > 0 {
			if err := tx.ensureAccount(policies); err != nil {
				return err
			}
			if err := tx.ensureScopes(scopes, policies); err != nil {
				return err
			}
			if _, err = tx.book(r.WorkloadKey(), financial.ComputeMonth(tx.now)); errors.Is(err, ErrNotFound) {
				err = tx.initialize(r.WorkloadKey(), false)
				if err == nil {
					err = computeMarkEnvironmentOnly(tx, r.WorkloadKey(), financial.ComputeMonth(tx.now), policies)
				}
			}
			if err != nil {
				return err
			}
		}
		out, err = computeAcquire(tx, r)
		return err
	})
	return out, err
}
func (s *PgStore) RenewFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	err = s.computeTransaction(ctx, r.AccountID, func(tx *pgFinancialComputeTxn) error { out, err = computeMutate(tx, r, false); return err })
	return out, err
}
func (s *PgStore) SettleFinancialCompute(ctx context.Context, r financial.ComputeRequest) (out financial.ComputeLease, err error) {
	err = s.computeTransaction(ctx, r.AccountID, func(tx *pgFinancialComputeTxn) error { out, err = computeMutate(tx, r, true); return err })
	return out, err
}
func (s *PgStore) RegisterFinancialComputeOwner(ctx context.Context, node, incarnation string) error {
	if uuid.Validate(node) != nil || uuid.Validate(incarnation) != nil {
		return ErrInvalidArgument
	}
	n, _ := parsePgUUID(node)
	i, _ := parsePgUUID(incarnation)
	return sqlc.New().FinancialComputeOwnerPut(ctx, s.pool, sqlc.FinancialComputeOwnerPutParams{NodeID: n, Incarnation: i, AccountComputeIncarnation: i, JobComputeIncarnation: i, EgressBudgetIncarnation: i, ScopeComputeIncarnation: i})
}

func (s *PgStore) FinancialComputeOwnerValid(ctx context.Context, node, incarnation string) (bool, error) {
	n, _ := parsePgUUID(node)
	i, _ := parsePgUUID(incarnation)
	return sqlc.New().FinancialComputeOwnerValid(ctx, s.pool, sqlc.FinancialComputeOwnerValidParams{NodeID: n, Incarnation: i})
}

func (s *PgStore) FinancialComputeRetired(ctx context.Context, node, instance string) (bool, error) {
	n, err := parsePgUUID(node)
	if err != nil {
		return false, ErrInvalidArgument
	}
	return sqlc.New().FinancialComputeRetired(ctx, s.pool, sqlc.FinancialComputeRetiredParams{NodeID: n, InstanceID: instance})
}

func (s *PgStore) FinancialComputeLease(ctx context.Context, account, id string) (financial.ComputeLease, bool, error) {
	a, accountErr := parsePgUUID(account)
	i, idErr := parsePgUUID(id)
	if accountErr != nil || idErr != nil {
		return financial.ComputeLease{}, false, ErrInvalidArgument
	}
	raw, err := sqlc.New().FinancialComputeLeaseGet(ctx, s.pool, sqlc.FinancialComputeLeaseGetParams{AccountID: a, ID: i})
	if errors.Is(err, pgx.ErrNoRows) {
		return financial.ComputeLease{}, false, nil
	}
	if err != nil {
		return financial.ComputeLease{}, false, err
	}
	var l financial.ComputeLease
	err = json.Unmarshal(raw, &l)
	return l, err == nil, err
}

func (s *PgStore) FinancialComputeInstanceLease(ctx context.Context, account, app, instance string) (financial.ComputeLease, bool, error) {
	a, _ := parsePgUUID(account)
	p, job := computePgWorkloadIDs(app)
	raw, err := sqlc.New().FinancialComputeLatestLease(ctx, s.pool, sqlc.FinancialComputeLatestLeaseParams{AccountID: a, AppID: p, JobID: job, InstanceID: instance})
	if errors.Is(err, pgx.ErrNoRows) {
		return financial.ComputeLease{}, false, nil
	}
	if err != nil {
		return financial.ComputeLease{}, false, err
	}
	var l financial.ComputeLease
	err = json.Unmarshal(raw, &l)
	return l, err == nil, err
}
func (s *PgStore) FinancialComputeAppManaged(ctx context.Context, account, app string) (bool, error) {
	a, _ := parsePgUUID(account)
	p, job := computePgWorkloadIDs(app)
	managed, err := sqlc.New().FinancialComputeAppManaged(ctx, s.pool, sqlc.FinancialComputeAppManagedParams{AccountID: a, AppID: p, JobID: job})
	if err != nil || managed.Valid && managed.Bool || !p.Valid {
		return managed.Valid && managed.Bool, err
	}
	// ADR-1750, amendments 1 and 2: an app in a capped project, or with a
	// capped environment in its project, is managed too.
	return sqlc.New().FinancialComputeProjectManaged(ctx, s.pool, sqlc.FinancialComputeProjectManagedParams{AccountID: a, AppID: p})
}
func (s *PgStore) FinancialComputeNodeLeases(ctx context.Context, node string) ([]financial.ComputeLease, error) {
	if uuid.Validate(node) != nil {
		return nil, ErrInvalidArgument
	}
	n, _ := parsePgUUID(node)
	return decodeComputeLeases(sqlc.New().FinancialComputeNodeLeases(ctx, s.pool, n))
}
func (s *PgStore) FinancialComputeBalance(ctx context.Context, p FinancialBudget, now time.Time) (out financial.ComputeBalance, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, _ := parsePgUUID(p.AccountID)
	reader := &pgFinancialComputeTxn{ctx: ctx, db: tx, q: sqlc.New(), account: a, now: time.Now().UTC()}
	out, err = computeBalance(reader, p, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) FinancialComputePolicies(ctx context.Context, account, app string) ([]FinancialBudget, error) {
	a, err := parsePgUUID(account)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	reader := &pgFinancialComputeTxn{ctx: ctx, db: s.pool, q: sqlc.New(), account: a}
	if !computeAppScopeKey(app) {
		// No app row scopes this workload, so the budgets are the one read.
		return computeWorkloadPolicies(reader, app)
	}
	// One statement reads the budgets and, only when there are any, the app's
	// project: the check costs one round trip on every admission (scheduler
	// wake path), and an account without budgets reads no app row.
	id, _ := parsePgUUID(app)
	rows, err := reader.q.FinancialComputeAppPolicies(ctx, s.pool, sqlc.FinancialComputeAppPoliciesParams{AccountID: a, AppID: id, PageSize: api.FinancialBudgetsPerAccount + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []FinancialBudget{}, nil
	}
	all := make([]FinancialBudget, 0, len(rows))
	for _, row := range rows {
		p, err := financialBudgetFromRow(row.FinancialBudgetPolicy)
		if err != nil {
			return nil, err
		}
		all = append(all, p)
	}
	return computeWorkloadPoliciesIn(reader, app, all, computeScopes{Project: rows[0].ProjectID})
}
func (s *PgStore) FinancialComputeBillingSeconds(ctx context.Context, account, app, instance string, start time.Time, seconds int64) (int64, error) {
	a, _ := parsePgUUID(account)
	p, job := computePgWorkloadIDs(app)
	rows, err := decodeComputeLeases(sqlc.New().FinancialComputeBillingLeases(ctx, s.pool, sqlc.FinancialComputeBillingLeasesParams{AccountID: a, AppID: p, JobID: job, InstanceID: instance, PeriodStart: pgTime(start), PeriodEnd: pgTime(start.Add(time.Minute))}))
	if err != nil {
		return 0, err
	}
	covered, err := s.FinancialComputeFloorCovered(ctx, account, app, start)
	if err == nil && !covered {
		reader := &pgFinancialComputeTxn{ctx: ctx, db: s.pool, q: sqlc.New(), account: a}
		covered, err = computeEnvironmentCovered(reader, app, instance, start)
	}
	if err != nil {
		return 0, err
	}
	prices, err := s.ListFinancialPriceSnapshots(ctx, account, financial.ComputeMonth(start))
	if err != nil {
		return 0, err
	}
	var latest FinancialPriceSnapshot
	for _, p := range prices {
		if p.Price.Meter == "compute" && !p.EffectiveFrom.After(start) && (latest.Price.Version == "" || p.EffectiveFrom.After(latest.EffectiveFrom) || p.EffectiveFrom.Equal(latest.EffectiveFrom) && p.RecordedAt.After(latest.RecordedAt)) {
			latest = p
		}
	}
	return computeBillingSeconds(rows, start, seconds, latest.Price, covered), nil
}

func (s *PgStore) FinancialComputeFloorCovered(ctx context.Context, account, app string, at time.Time) (bool, error) {
	a, _ := parsePgUUID(account)
	reader := &pgFinancialComputeTxn{ctx: ctx, db: s.pool, q: sqlc.New(), account: a}
	_, err := reader.book("", financial.ComputeMonth(at))
	if errors.Is(err, ErrNotFound) {
		var b financial.ComputeBook
		if b, err = reader.book(app, financial.ComputeMonth(at)); err == nil && b.EnvironmentOnly {
			err = ErrNotFound
		}
	}
	scopes, scopeErr := reader.workloadScopes(app, "", "")
	if errors.Is(err, ErrNotFound) && scopeErr == nil && scopes.Project != "" {
		_, err = reader.book(financial.ComputeProjectKey(scopes.Project), financial.ComputeMonth(at))
	}
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	policies, err := computeFloorPolicies(reader, app)
	if err != nil {
		return false, err
	}
	for _, p := range policies {
		if p.UpdatedAt.Before(financial.ComputeMonth(at).AddDate(0, 1, 0)) {
			return true, nil
		}
	}
	return false, nil
}
func (s *PgStore) FinancialComputeReady(ctx context.Context, account, app string) (bool, error) {
	a, _ := parsePgUUID(account)
	tx := &pgFinancialComputeTxn{ctx: ctx, db: s.pool, q: sqlc.New(), account: a, now: time.Now().UTC()}
	return tx.ready(app, true)
}
func (t *pgFinancialComputeTxn) ready(app string, quiet bool) (bool, error) {
	if job, ok := computeJobKey(app); ok {
		return t.jobReady(job)
	}
	if scope, ok := computeSharedScopeOf(app); ok {
		return t.scopeReady(scope, quiet)
	}
	if app == "" {
		return t.accountReady()
	}
	ready, err := t.q.FinancialComputeOwnersReady(t.ctx, t.db, 3*api.FinancialComputeOwnerHeartbeat.Seconds())
	if err != nil || !ready.Valid || !ready.Bool {
		return false, err
	}
	id, _ := parsePgUUID(app)
	if quiet {
		safe, err := t.q.FinancialComputeActivationSafe(t.ctx, t.db, sqlc.FinancialComputeActivationSafeParams{AccountID: t.account, AppID: id})
		if err != nil || !safe {
			return false, err
		}
	} else {
		active, err := t.active(app)
		if err != nil || len(active) > 0 {
			return false, err
		}
	}
	readySampling, err := t.q.FinancialComputeSamplingReady(t.ctx, t.db, sqlc.FinancialComputeSamplingReadyParams{AccountID: t.account, AppID: id, PeriodStart: pgTime(financial.ComputeMonth(t.now))})
	return readySampling.Valid && readySampling.Bool, err
}
func (t *pgFinancialComputeTxn) initialize(app string, quiet bool) error {
	if job, ok := computeJobKey(app); ok {
		return t.initializeJob(job)
	}
	if scope, ok := computeSharedScopeOf(app); ok {
		return t.initializeScope(scope, quiet)
	}
	if app == "" {
		return t.initializeAccount()
	}
	ready, err := t.ready(app, quiet)
	if err != nil {
		return err
	}
	if !ready {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.book(app, month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: pgUUIDString(t.account), AppID: app, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	baseline, err := t.q.FinancialComputeBaseline(t.ctx, t.db, sqlc.FinancialComputeBaselineParams{AccountID: t.account, AppID: app, PeriodStart: pgTime(month), PeriodEnd: pgTime(b.PeriodEnd)})
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
	return t.putBook(b)
}

func computeBaselineAmount(quantity int64, p financial.Price) (int64, error) {
	if p.Meter != "compute" || p.Currency != financial.BillingCurrency || p.Unit != "mb_seconds" || p.UnitQuantity <= 0 || p.MillicentsPerUnit < 0 {
		return 0, financial.ErrComputeUnavailable
	}
	n := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(p.MillicentsPerUnit))
	d := big.NewInt(p.UnitQuantity)
	n.Add(n, new(big.Int).Sub(d, big.NewInt(1))).Quo(n, d)
	if !n.IsInt64() {
		return 0, fmt.Errorf("compute baseline: %w", financial.ErrOverflow)
	}
	amount := n.Int64()
	if quantity > 0 && p.MillicentsPerUnit > 0 {
		if amount >= api.FinancialBudgetMoneyMax {
			return 0, financial.ErrOverflow
		}
		amount++
	}
	return amount, nil
}
