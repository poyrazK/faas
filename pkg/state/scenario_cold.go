// adr: 20261017201013
package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func coldRow(r sqlc.ScenarioColdPreparationRow) rehearsalcold.Preparation {
	return rehearsalcold.Preparation{ID: r.ID, AccountID: r.AccountID, AppID: r.AppID, RunID: r.RunID, Workload: r.WorkloadName, DeploymentID: r.DeploymentID, InstanceID: r.InstanceID, RecorderSessionID: r.RecorderSessionID, State: r.State, BootInstanceID: r.BootInstanceID, BootRecorderSessionID: r.BootRecorderSessionID, WakeID: r.WakeID, Method: r.Method, StartupMS: r.StartupMs}
}

func (s *PgStore) ScenarioColdPreparation(ctx context.Context, app string) (rehearsalcold.Preparation, error) {
	r, e := sqlc.New().ScenarioColdPreparation(ctx, s.pool, app)
	return coldRow(r), mapErr(e)
}

type coldInstance struct {
	ID           string `json:"id"`
	DeploymentID string `json:"deployment_id"`
	State        string `json:"state"`
	TailCount    int    `json:"tail_count"`
	Recorder     string `json:"recorder"`
}

func (s *PgStore) BeginScenarioColdPreparation(ctx context.Context, account, app string) (rehearsalcold.Preparation, error) {
	var empty rehearsalcold.Preparation
	member, err := s.ScenarioTestMemberByApp(ctx, app)
	if err != nil {
		return empty, err
	}
	if member.AccountID != account || member.ColdStartJSON == "" {
		return empty, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: account, RunID: member.RunID}); err != nil {
		return empty, err
	}
	eligible, err := q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: account, AppID: app})
	if err != nil {
		return empty, mapErr(err)
	}
	if !eligible.Valid || !eligible.Bool {
		return empty, ErrConflict
	}
	if row, e := q.ScenarioColdPreparation(ctx, tx, app); e == nil {
		if row.State == "failed" || row.State == "booted" {
			return empty, ErrConflict
		}
		return coldRow(row), tx.Commit(ctx)
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return empty, e
	}
	inv, err := q.ScenarioColdInventory(ctx, tx, app)
	if err != nil {
		return empty, err
	}
	var live []coldInstance
	if inv.AccountID != account || inv.RunID != member.RunID || len(inv.ColdStartSpec) == 0 || inv.SealedAt.Valid || inv.RecorderClosing || inv.SimulationFailed || (!inv.Busy.Valid || inv.Busy.Bool) || len(inv.Deployments) != 1 || json.Unmarshal(inv.Instances, &live) != nil || len(live) > 1 {
		return empty, ErrConflict
	}
	p := rehearsalcold.Preparation{ID: uuid.NewString(), AccountID: account, AppID: app, RunID: member.RunID, Workload: member.Workload, DeploymentID: inv.Deployments[0], State: "retired"}
	if len(live) == 1 {
		i := live[0]
		if i.State != "running" || i.TailCount != 0 || i.DeploymentID != p.DeploymentID || i.Recorder == "" {
			return empty, ErrConflict
		}
		p.InstanceID, p.RecorderSessionID, p.State = i.ID, i.Recorder, "armed"
	}
	err = q.InsertScenarioColdPreparation(ctx, tx, sqlc.InsertScenarioColdPreparationParams{ID: p.ID, AccountID: account, AppID: app, RunID: p.RunID, DeploymentID: p.DeploymentID, InstanceID: p.InstanceID, SessionID: p.RecorderSessionID, State: p.State})
	if err != nil {
		return empty, mapErr(err)
	}
	return p, tx.Commit(ctx)
}

func (s *PgStore) ClaimScenarioColdPreparation(ctx context.Context, app string) (rehearsalcold.Preparation, error) {
	tx, _, e := coldControlTransaction(ctx, s, app)
	if e != nil {
		return rehearsalcold.Preparation{}, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	n, e := sqlc.New().TransitionScenarioColdPreparation(ctx, tx, sqlc.TransitionScenarioColdPreparationParams{AppID: app, Expected: "armed", State: "claimed"})
	if e != nil {
		return rehearsalcold.Preparation{}, e
	}
	if n != 1 {
		return rehearsalcold.Preparation{}, ErrConflict
	}
	r, e := sqlc.New().ScenarioColdPreparation(ctx, tx, app)
	if e != nil {
		return rehearsalcold.Preparation{}, e
	}
	return coldRow(r), tx.Commit(ctx)
}

func (s *PgStore) CompleteScenarioColdPreparation(ctx context.Context, account, app string) error {
	tx, r, e := coldControlTransaction(ctx, s, app)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	p := coldRow(r)
	if p.AccountID != account {
		return ErrNotFound
	}
	if p.State == "ready" {
		return tx.Commit(ctx)
	}
	n, e := sqlc.New().TransitionScenarioColdPreparation(ctx, tx, sqlc.TransitionScenarioColdPreparationParams{AppID: app, Expected: "retired", State: "ready"})
	if e != nil {
		return mapErr(e)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
func (s *PgStore) FailScenarioColdPreparation(ctx context.Context, app string) error {
	tx, _, err := coldControlTransaction(ctx, s, app)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().FailScenarioColdPreparation(ctx, tx, app); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Control requests take the same run/app locks as recorder settlement before
// taking a preparation row lock, including timeout-driven failure paths.
func coldControlTransaction(ctx context.Context, s *PgStore, app string) (pgx.Tx, sqlc.ScenarioColdPreparationRow, error) {
	var empty sqlc.ScenarioColdPreparationRow
	p, err := s.ScenarioColdPreparation(ctx, app)
	if err != nil {
		return nil, empty, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, empty, err
	}
	q := sqlc.New()
	if err = q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: p.AccountID, RunID: p.RunID}); err == nil {
		_, err = q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: p.AccountID, AppID: app})
	}
	var r sqlc.ScenarioColdPreparationRow
	if err == nil {
		r, err = q.ScenarioColdPreparation(ctx, tx, app)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, empty, mapErr(err)
	}
	return tx, r, nil
}

func coldIdentity(p rehearsalcold.Preparation, id rehearsalrecorder.Identity, preparation, session string) bool {
	return p.ID == preparation && p.AccountID == id.AccountID && p.AppID == id.AppID && p.RunID == id.RunID && p.DeploymentID == id.DeploymentID && p.InstanceID == id.InstanceID && p.RecorderSessionID == session
}

func (s *PgStore) coldRetirement(ctx context.Context, id rehearsalrecorder.Identity, preparation, session, expected, next string) error {
	tx, policy, e := recorderTransaction(ctx, s, id)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, e := q.ScenarioColdPreparation(ctx, tx, id.AppID)
	if e != nil {
		return mapErr(e)
	}
	if !coldIdentity(coldRow(row), id, preparation, session) || policy.RecorderClosing || row.State != expected {
		return ErrConflict
	}
	r, e := q.ScenarioRecorderSession(ctx, tx, recorderSessionParams(id, session))
	if e != nil {
		return mapErr(e)
	}
	if r.Status != "recording" || r.Failed != 0 || r.Pending != 0 {
		return ErrConflict
	}
	n, e := q.TransitionScenarioColdPreparation(ctx, tx, sqlc.TransitionScenarioColdPreparationParams{AppID: id.AppID, Expected: expected, State: next})
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	if next == "retired" {
		if e = q.SetScenarioRecorderStatus(ctx, tx, sqlc.SetScenarioRecorderStatusParams{ID: session, Status: "closed"}); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *PgStore) FenceScenarioColdRetirement(ctx context.Context, id rehearsalrecorder.Identity, prep, session string) error {
	return s.coldRetirement(ctx, id, prep, session, "claimed", "fenced")
}
func (s *PgStore) CompleteScenarioColdRetirement(ctx context.Context, id rehearsalrecorder.Identity, prep, session string) error {
	return s.coldRetirement(ctx, id, prep, session, "fenced", "retired")
}

func (s *PgStore) ScenarioColdBootRequired(ctx context.Context, id rehearsalrecorder.Identity) (bool, error) {
	p, e := s.ScenarioColdPreparation(ctx, id.AppID)
	if errors.Is(e, ErrNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return coldBootRequired(p, id)
}
func coldBootRequired(p rehearsalcold.Preparation, id rehearsalrecorder.Identity) (bool, error) {
	if p.AccountID != id.AccountID || p.RunID != id.RunID || p.DeploymentID != id.DeploymentID {
		return false, ErrConflict
	}
	if p.InstanceID == id.InstanceID && (p.State == "armed" || p.State == "claimed") {
		return false, nil
	}
	if p.State != "ready" || p.InstanceID == id.InstanceID {
		return false, ErrConflict
	}
	return true, nil
}
func (s *PgStore) RecordScenarioColdBoot(ctx context.Context, id rehearsalrecorder.Identity, session, method string, ms int64) error {
	p, e := s.ScenarioColdPreparation(ctx, id.AppID)
	if errors.Is(e, ErrNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	if required, err := coldBootRequired(p, id); err != nil {
		return err
	} else if !required {
		return nil
	}
	if method != "cold_boot" || ms < 0 {
		return ErrConflict
	}
	tx, policy, e := recorderTransaction(ctx, s, id)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if policy.RecorderClosing || !policy.Active.Valid || !policy.Active.Bool {
		return ErrConflict
	}
	n, e := sqlc.New().RecordScenarioColdBoot(ctx, tx, sqlc.RecordScenarioColdBootParams{AppID: id.AppID, DeploymentID: id.DeploymentID, InstanceID: id.InstanceID, SessionID: session, Method: method, StartupMs: ms})
	if e != nil {
		return mapErr(e)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
