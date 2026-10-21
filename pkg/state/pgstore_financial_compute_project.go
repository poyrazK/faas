// adr: 1750 — amendment 1: strict project books under the account lock.
package state

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (t *pgFinancialComputeTxn) workloadProject(key string) (string, error) {
	if _, ok := computeJobKey(key); ok || key == "" {
		return "", nil
	}
	if _, ok := financial.ComputeProjectID(key); ok {
		return "", nil
	}
	app, err := parsePgUUID(key)
	if err != nil {
		return "", nil
	}
	project, err := t.q.FinancialComputeAppProject(t.ctx, t.db, sqlc.FinancialComputeAppProjectParams{AccountID: t.account, AppID: app})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return project, err
}

func (t *pgFinancialComputeTxn) projectBook(project string, month time.Time) (financial.ComputeBook, error) {
	id, err := parsePgUUID(project)
	if err != nil {
		return financial.ComputeBook{}, ErrInvalidArgument
	}
	raw, err := t.q.FinancialComputeProjectBookGet(t.ctx, t.db, sqlc.FinancialComputeProjectBookGetParams{AccountID: t.account, ProjectID: id, PeriodStart: pgTime(month)})
	var b financial.ComputeBook
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &b)
	}
	return b, err
}

func (t *pgFinancialComputeTxn) putProjectBook(b financial.ComputeBook) error {
	id, err := parsePgUUID(b.ProjectID)
	if err != nil {
		return ErrInvalidArgument
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return t.q.FinancialComputeProjectBookPut(t.ctx, t.db, sqlc.FinancialComputeProjectBookPutParams{AccountID: t.account, ProjectID: id, PeriodStart: pgTime(b.PeriodStart), PeriodEnd: pgTime(b.PeriodEnd), Payload: raw})
}

func (t *pgFinancialComputeTxn) projectLeases(project string) ([]financial.ComputeLease, error) {
	id, err := parsePgUUID(project)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	return decodeComputeLeases(t.q.FinancialComputeProjectLeases(t.ctx, t.db, sqlc.FinancialComputeProjectLeasesParams{AccountID: t.account, ProjectID: id}))
}

// projectReady requires capable owners, complete sampling since the project
// or month started and no unsettled project attempt; with quiet, also no
// running instance, task or open billing interval of a project app.
func (t *pgFinancialComputeTxn) projectReady(project string, quiet bool) (bool, error) {
	id, err := parsePgUUID(project)
	if err != nil {
		return false, ErrInvalidArgument
	}
	owners, err := t.q.FinancialComputeOwnersReady(t.ctx, t.db, 3*api.FinancialComputeOwnerHeartbeat.Seconds())
	if err != nil || !owners.Valid || !owners.Bool {
		return false, err
	}
	if quiet {
		safe, err := t.q.FinancialComputeProjectActivationSafe(t.ctx, t.db, sqlc.FinancialComputeProjectActivationSafeParams{ProjectID: id, AccountID: t.account})
		if err != nil || !safe.Valid || !safe.Bool {
			return false, err
		}
	} else {
		active, err := t.projectLeases(project)
		if err != nil || len(active) > 0 {
			return false, err
		}
	}
	ready, err := t.q.FinancialComputeProjectSamplingReady(t.ctx, t.db, sqlc.FinancialComputeProjectSamplingReadyParams{PeriodStart: pgTime(financial.ComputeMonth(t.now)), ProjectID: id, AccountID: t.account})
	return ready.Valid && ready.Bool, err
}

func (t *pgFinancialComputeTxn) initializeProject(project string, quiet bool) error {
	ready, err := t.projectReady(project, quiet)
	if err != nil {
		return err
	}
	if !ready {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.projectBook(project, month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: pgUUIDString(t.account), ProjectID: project, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	baseline, err := t.q.FinancialComputeProjectBaseline(t.ctx, t.db, sqlc.FinancialComputeProjectBaselineParams{PeriodStart: pgTime(month), AccountID: t.account, ProjectID: project, PeriodEnd: pgTime(b.PeriodEnd)})
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
	return t.putProjectBook(b)
}

// ensureProject opens the month's project book for a capped project.
func (t *pgFinancialComputeTxn) ensureProject(project string, policies []FinancialBudget) error {
	for _, p := range policies {
		if financial.StrictProjectCompute(p.Spec) && p.Spec.Scope.ID == project {
			if _, err := t.projectBook(project, financial.ComputeMonth(t.now)); !errors.Is(err, ErrNotFound) {
				return err
			}
			return t.initializeProject(project, false)
		}
	}
	return nil
}
