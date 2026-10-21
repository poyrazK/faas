// adr: 1750 — amendments 1 and 2: strict project and environment books under
// the account lock.
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

func (t *pgFinancialComputeTxn) workloadScopes(key, instance, deployment string) (computeScopes, error) {
	if _, ok := computeJobKey(key); ok || key == "" {
		return computeScopes{}, nil
	}
	if _, ok := computeSharedScopeOf(key); ok {
		return computeScopes{}, nil
	}
	// A key that is not a UUID names no app row, so it has no scopes.
	if !validUUID(key) {
		return computeScopes{}, nil
	}
	app, _ := parsePgUUID(key)
	// Instance IDs are opaque to the ledger; only a UUID can name a row.
	ins, _ := parsePgUUID(instance)
	dep, _ := parsePgUUID(deployment)
	row, err := t.q.FinancialComputeAppScopes(t.ctx, t.db, sqlc.FinancialComputeAppScopesParams{AccountID: t.account, AppID: app, InstanceID: ins, DeploymentID: dep})
	if errors.Is(err, pgx.ErrNoRows) {
		return computeScopes{}, nil
	}
	return computeScopes{Project: row.ProjectID, Environment: row.EnvironmentID, Deployment: row.DeploymentID}, err
}

func (t *pgFinancialComputeTxn) projectEnvironments(project string) ([]string, error) {
	if !validUUID(project) {
		return nil, nil
	}
	id, _ := parsePgUUID(project)
	return t.q.FinancialComputeProjectEnvironments(t.ctx, t.db, sqlc.FinancialComputeProjectEnvironmentsParams{AccountID: t.account, ProjectID: id})
}

func (t *pgFinancialComputeTxn) scopeBook(s computeSharedScope, month time.Time) (financial.ComputeBook, error) {
	id, err := parsePgUUID(s.id)
	if err != nil {
		return financial.ComputeBook{}, ErrInvalidArgument
	}
	raw, err := t.q.FinancialComputeScopeBookGet(t.ctx, t.db, sqlc.FinancialComputeScopeBookGetParams{AccountID: t.account, ScopeKind: s.kind, ScopeID: id, PeriodStart: pgTime(month)})
	var b financial.ComputeBook
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &b)
	}
	return b, err
}

func (t *pgFinancialComputeTxn) putScopeBook(b financial.ComputeBook) error {
	s, _ := computeSharedScopeOf(b.ScopeKey())
	id, err := parsePgUUID(s.id)
	if err != nil {
		return ErrInvalidArgument
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return t.q.FinancialComputeScopeBookPut(t.ctx, t.db, sqlc.FinancialComputeScopeBookPutParams{AccountID: t.account, ScopeKind: s.kind, ScopeID: id, PeriodStart: pgTime(b.PeriodStart), PeriodEnd: pgTime(b.PeriodEnd), Payload: raw})
}

func (t *pgFinancialComputeTxn) scopeLeases(s computeSharedScope) ([]financial.ComputeLease, error) {
	id, err := parsePgUUID(s.id)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if s.kind == "project" {
		return decodeComputeLeases(t.q.FinancialComputeProjectLeases(t.ctx, t.db, sqlc.FinancialComputeProjectLeasesParams{AccountID: t.account, ProjectID: id}))
	}
	return decodeComputeLeases(t.q.FinancialComputeEnvironmentLeases(t.ctx, t.db, sqlc.FinancialComputeEnvironmentLeasesParams{AccountID: t.account, EnvironmentID: id}))
}

// scopeReady requires capable owners, complete sampling since the scope or
// month started and no unsettled attempt in it; with quiet, also no running
// instance, task or open billing interval in it.
func (t *pgFinancialComputeTxn) scopeReady(s computeSharedScope, quiet bool) (bool, error) {
	id, err := parsePgUUID(s.id)
	if err != nil {
		return false, ErrInvalidArgument
	}
	owners, err := t.q.FinancialComputeOwnersReady(t.ctx, t.db, 3*api.FinancialComputeOwnerHeartbeat.Seconds())
	if err != nil || !owners.Valid || !owners.Bool {
		return false, err
	}
	if quiet {
		var safe pgtype.Bool
		if s.kind == "project" {
			safe, err = t.q.FinancialComputeProjectActivationSafe(t.ctx, t.db, sqlc.FinancialComputeProjectActivationSafeParams{ProjectID: id, AccountID: t.account})
		} else {
			safe, err = t.q.FinancialComputeEnvironmentActivationSafe(t.ctx, t.db, sqlc.FinancialComputeEnvironmentActivationSafeParams{EnvironmentID: id, AccountID: t.account})
		}
		if err != nil || !safe.Valid || !safe.Bool {
			return false, err
		}
	} else {
		active, err := t.scopeLeases(s)
		if err != nil || len(active) > 0 {
			return false, err
		}
	}
	month := pgTime(financial.ComputeMonth(t.now))
	var ready pgtype.Bool
	if s.kind == "project" {
		ready, err = t.q.FinancialComputeProjectSamplingReady(t.ctx, t.db, sqlc.FinancialComputeProjectSamplingReadyParams{PeriodStart: month, ProjectID: id, AccountID: t.account})
	} else {
		ready, err = t.q.FinancialComputeEnvironmentSamplingReady(t.ctx, t.db, sqlc.FinancialComputeEnvironmentSamplingReadyParams{PeriodStart: month, EnvironmentID: id, AccountID: t.account})
	}
	return ready.Valid && ready.Bool, err
}

// initializeScope seeds the month's shared book with the scope's retained
// gross charges, as initialize does for one app.
func (t *pgFinancialComputeTxn) initializeScope(s computeSharedScope, quiet bool) error {
	ready, err := t.scopeReady(s, quiet)
	if err != nil {
		return err
	}
	if !ready {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.scopeBook(s, month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: pgUUIDString(t.account), PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
		if s.kind == "project" {
			b.ProjectID = s.id
		} else {
			b.EnvironmentID = s.id
		}
	} else if err != nil {
		return err
	}
	amount, incomplete, err := t.scopeBaseline(s, month, b.PeriodEnd)
	if err != nil {
		return err
	}
	if incomplete {
		return financial.ErrComputeUnavailable
	}
	total, err := strconv.ParseInt(amount, 10, 64)
	if err != nil || total < 0 || total > api.FinancialBudgetMoneyMax {
		return financial.ErrOverflow
	}
	b.Committed = max(b.Committed, total)
	return t.putScopeBook(b)
}

func (t *pgFinancialComputeTxn) scopeBaseline(s computeSharedScope, month, end time.Time) (string, bool, error) {
	if s.kind == "project" {
		row, err := t.q.FinancialComputeProjectBaseline(t.ctx, t.db, sqlc.FinancialComputeProjectBaselineParams{PeriodStart: pgTime(month), AccountID: t.account, ProjectID: s.id, PeriodEnd: pgTime(end)})
		return row.Amount, row.Incomplete, err
	}
	id, err := parsePgUUID(s.id)
	if err != nil {
		return "", false, ErrInvalidArgument
	}
	row, err := t.q.FinancialComputeEnvironmentBaseline(t.ctx, t.db, sqlc.FinancialComputeEnvironmentBaselineParams{PeriodStart: pgTime(month), AccountID: t.account, EnvironmentID: id, PeriodEnd: pgTime(end)})
	return row.Amount, row.Incomplete, err
}

func (t *pgFinancialComputeTxn) ensureScopes(scopes computeScopes, policies []FinancialBudget) error {
	return computeEnsureScopes(t, scopes, policies, t.initialize)
}

// FinancialComputeDeploymentEnvironment resolves the environment whose cap a
// deployment's attempts fund ("" when none; ADR-1750, amendment 2).
func (s *PgStore) FinancialComputeDeploymentEnvironment(ctx context.Context, account, app, deployment string) (string, error) {
	a, errA := parsePgUUID(account)
	p, errP := parsePgUUID(app)
	d, errD := parsePgUUID(deployment)
	if errA != nil || errP != nil || errD != nil {
		return "", ErrInvalidArgument
	}
	env, err := sqlc.New().FinancialComputeDeploymentEnvironment(ctx, s.pool, sqlc.FinancialComputeDeploymentEnvironmentParams{AccountID: a, AppID: p, DeploymentID: d})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return env, err
}
