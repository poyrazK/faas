// adr: 20261017201014
package state

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func suspendRow(r sqlc.ScenarioSuspendResumeRow) rehearsalresume.Receipt {
	return rehearsalresume.Receipt{ID: r.ID, AccountID: r.AccountID, AppID: r.AppID, RunID: r.RunID, Workload: r.WorkloadName, DeploymentID: r.DeploymentID, InstanceID: r.InstanceID, RecorderSessionID: r.RecorderSessionID, InvocationID: r.InvocationID, InvocationAttempt: int(r.InvocationAttempt), CheckpointAttemptID: r.CheckpointAttemptID, State: r.State, Method: r.Method, PauseMS: r.PauseMs, ResumeMS: r.ResumeMs}
}

func (s *PgStore) ScenarioSuspendResume(ctx context.Context, id string) (rehearsalresume.Receipt, error) {
	r, err := sqlc.New().ScenarioSuspendResume(ctx, s.pool, id)
	return suspendRow(r), mapErr(err)
}

func newSuspendEvidence(run, raw string, inventory []string) rehearsalresume.Evidence {
	spec := rehearsalresume.Decode(raw)
	_, digest, _ := rehearsalresume.Normalize(spec, inventory)
	return rehearsalresume.Evidence{Version: 1, RunID: run, Spec: spec, PlanSHA256: digest, Status: "pending", Receipts: []rehearsalresume.Receipt{}}
}

func finishSuspendEvidence(e *rehearsalresume.Evidence, inventory []string) {
	slices.SortFunc(e.Receipts, func(a, b rehearsalresume.Receipt) int {
		if a.Workload < b.Workload {
			return -1
		}
		if a.Workload > b.Workload {
			return 1
		}
		return 0
	})
	e.Status = "complete"
	if e.Validate(inventory) != nil {
		e.Status = "pending"
		for _, r := range e.Receipts {
			if r.State == "failed" {
				e.Status = "incomplete"
			}
		}
	}
}

func suspendEvidenceTx(ctx context.Context, db sqlc.DBTX, account, run string, policies []sqlc.ScenarioActionPoliciesForRunRow) (rehearsalresume.Evidence, error) {
	var empty rehearsalresume.Evidence
	if len(policies) == 0 || len(policies[0].SuspendResumeSpec) == 0 {
		return empty, ErrNotFound
	}
	names := make([]string, 0, len(policies))
	for _, p := range policies {
		names = append(names, p.WorkloadName)
	}
	e := newSuspendEvidence(run, string(policies[0].SuspendResumeSpec), names)
	for _, p := range policies {
		if string(p.SuspendResumeSpec) != string(policies[0].SuspendResumeSpec) {
			return empty, ErrConflict
		}
	}
	q := sqlc.New()
	ids, err := q.ScenarioSuspendResumesForRun(ctx, db, sqlc.ScenarioSuspendResumesForRunParams{AccountID: account, RunID: run})
	if err != nil {
		return empty, err
	}
	for _, id := range ids {
		r, readErr := q.ScenarioSuspendResume(ctx, db, id)
		if readErr != nil {
			return empty, readErr
		}
		e.Receipts = append(e.Receipts, suspendRow(r))
	}
	finishSuspendEvidence(&e, names)
	return e, nil
}

func (s *PgStore) ScenarioSuspendResumeEvidence(ctx context.Context, account, run string) (rehearsalresume.Evidence, error) {
	policies, err := sqlc.New().ScenarioActionPoliciesForRun(ctx, s.pool, sqlc.ScenarioActionPoliciesForRunParams{AccountID: account, RunID: run})
	if err != nil {
		return rehearsalresume.Evidence{}, mapErr(err)
	}
	return suspendEvidenceTx(ctx, s.pool, account, run, policies)
}

func (s *PgStore) BeginScenarioSuspendResume(ctx context.Context, account, run string) (rehearsalresume.Evidence, error) {
	var empty rehearsalresume.Evidence
	if !scenarioTestRunPattern.MatchString(run) {
		return empty, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: account, RunID: run}); err != nil {
		return empty, err
	}
	policies, err := q.ScenarioActionPoliciesForRun(ctx, tx, sqlc.ScenarioActionPoliciesForRunParams{AccountID: account, RunID: run})
	if err != nil {
		return empty, err
	}
	e, err := suspendEvidenceTx(ctx, tx, account, run, policies)
	if err != nil {
		return empty, err
	}
	if e.Spec.ManagedJob() {
		return empty, ErrConflict
	}
	if len(e.Receipts) > 0 {
		if len(e.Receipts) != len(e.Spec.Workloads) {
			return empty, ErrConflict
		}
		return e, tx.Commit(ctx)
	}
	for _, p := range policies {
		if !slices.Contains(e.Spec.Workloads, p.WorkloadName) {
			continue
		}
		eligible, lockErr := q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: account, AppID: p.AppID})
		if lockErr != nil {
			return empty, mapErr(lockErr)
		}
		if !eligible.Valid || !eligible.Bool || p.SealedAt.Valid {
			return empty, ErrConflict
		}
		inv, readErr := q.ScenarioColdInventory(ctx, tx, p.AppID)
		if readErr != nil {
			return empty, readErr
		}
		live, parseErr := suspendInventory(inv.Instances)
		if parseErr != nil || len(inv.Deployments) != 1 || !inv.Busy.Valid || inv.Busy.Bool || inv.RecorderClosing || inv.SimulationFailed || len(live) != 1 || live[0].State != "running" || live[0].TailCount != 0 || live[0].Recorder == "" || live[0].DeploymentID != inv.Deployments[0] {
			return empty, ErrConflict
		}
		i := live[0]
		err = q.InsertScenarioSuspendResume(ctx, tx, sqlc.InsertScenarioSuspendResumeParams{ID: uuid.NewString(), AccountID: account, AppID: p.AppID, RunID: run, DeploymentID: i.DeploymentID, InstanceID: i.ID, SessionID: i.Recorder})
		if err != nil {
			return empty, mapErr(err)
		}
	}
	e, err = suspendEvidenceTx(ctx, tx, account, run, policies)
	if err != nil {
		return empty, err
	}
	return e, tx.Commit(ctx)
}

func (s *PgStore) PendingScenarioSuspendResumes(ctx context.Context) ([]rehearsalresume.Receipt, error) {
	ids, err := sqlc.New().PendingScenarioSuspendResumes(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	var out []rehearsalresume.Receipt
	for _, id := range ids {
		r, e := s.ScenarioSuspendResume(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *PgStore) suspendControlTransaction(ctx context.Context, id string) (pgx.Tx, rehearsalresume.Receipt, error) {
	r, err := s.ScenarioSuspendResume(ctx, id)
	if err != nil {
		return nil, r, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, r, err
	}
	q := sqlc.New()
	err = q.LockScenarioActionRun(ctx, tx, sqlc.LockScenarioActionRunParams{AccountID: r.AccountID, RunID: r.RunID})
	if err == nil {
		_, err = q.LockScenarioActionApp(ctx, tx, sqlc.LockScenarioActionAppParams{AccountID: r.AccountID, AppID: r.AppID})
	}
	if err == nil {
		var row sqlc.ScenarioSuspendResumeRow
		row, err = q.ScenarioSuspendResume(ctx, tx, id)
		r = suspendRow(row)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, r, mapErr(err)
	}
	return tx, r, nil
}

func suspendStep(r rehearsalresume.Receipt, expected, next string, pause, resume int64) (rehearsalresume.Receipt, error) {
	valid := map[string]string{"waiting": "armed", "armed": "claimed", "claimed": "fenced", "fenced": "paused", "paused": "resuming", "resuming": "resumed", "resumed": "complete"}
	if r.State != expected || valid[expected] != next || pause < 0 || resume < 0 {
		return r, ErrConflict
	}
	r.State = next
	if next == "resuming" {
		r.PauseMS = pause
	}
	if next == "resumed" {
		r.Method, r.ResumeMS = "in_place", resume
	}
	return r, nil
}

func suspendTransition(ctx context.Context, db sqlc.DBTX, r rehearsalresume.Receipt, expected, next string, pause, resume int64) error {
	if _, err := suspendStep(r, expected, next, pause, resume); err != nil {
		return err
	}
	n, err := sqlc.New().TransitionScenarioSuspendResume(ctx, db, sqlc.TransitionScenarioSuspendResumeParams{ID: r.ID, Expected: expected, State: next, PauseMs: pause, ResumeMs: resume})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ClaimScenarioSuspendResume(ctx context.Context, id string) (rehearsalresume.Receipt, error) {
	tx, r, err := s.suspendControlTransaction(ctx, id)
	if err != nil {
		return r, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = suspendTransition(ctx, tx, r, "armed", "claimed", 0, 0); err != nil {
		return r, err
	}
	r.State = "claimed"
	return r, tx.Commit(ctx)
}
func (s *PgStore) CompleteScenarioSuspendResume(ctx context.Context, id string) error {
	tx, r, err := s.suspendControlTransaction(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = suspendTransition(ctx, tx, r, "resumed", "complete", 0, 0); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) FailScenarioSuspendResume(ctx context.Context, id string) error {
	tx, _, err := s.suspendControlTransaction(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = sqlc.New().FailScenarioSuspendResume(ctx, tx, id); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func suspendIdentity(r rehearsalresume.Receipt, id rehearsalrecorder.Identity, session string) bool {
	return r.AccountID == id.AccountID && r.AppID == id.AppID && r.RunID == id.RunID && r.DeploymentID == id.DeploymentID && r.InstanceID == id.InstanceID && r.RecorderSessionID == session
}

func (s *PgStore) AdvanceScenarioSuspendResume(ctx context.Context, id rehearsalrecorder.Identity, session, receipt, expected, next string, pause, resume int64) (rehearsalresume.Spec, error) {
	var empty rehearsalresume.Spec
	if !suspendOwnerStep(expected, next) {
		return empty, ErrConflict
	}
	tx, r, err := s.suspendControlTransaction(ctx, receipt)
	if err != nil {
		return empty, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !suspendIdentity(r, id, session) {
		return empty, ErrConflict
	}
	q := sqlc.New()
	member, err := q.ScenarioTestMemberWithPolicy(ctx, tx, id.AppID)
	if err != nil {
		return empty, mapErr(err)
	}
	rc, err := q.ScenarioRecorderSession(ctx, tx, recorderSessionParams(id, session))
	if err != nil {
		return empty, mapErr(err)
	}
	if rc.Status != "recording" || rc.RecorderClosing || rc.Pending != 0 || rc.Failed != 0 {
		return empty, ErrConflict
	}
	if next == "armed" {
		pending, readErr := q.ScenarioSuspendCommandsPending(ctx, tx, sqlc.ScenarioSuspendCommandsPendingParams{AccountID: r.AccountID, RunID: r.RunID})
		if readErr != nil {
			return empty, readErr
		}
		if pending {
			return empty, rehearsalresume.ErrCheckpointBusy
		}
	}
	if next == "resuming" {
		proof, readErr := q.ReadRuntimeInstanceConfigProof(ctx, tx, sqlc.ReadRuntimeInstanceConfigProofParams{AccountID: mustPgUUID(id.AccountID), AppID: mustPgUUID(id.AppID), InstanceID: mustPgUUID(id.InstanceID)})
		if readErr != nil {
			return empty, mapErr(readErr)
		}
		values, readErr := runtimeAppValuesDB(ctx, tx, id.AccountID, id.AppID, id.DeploymentID)
		if readErr != nil {
			return empty, readErr
		}
		current, readErr := NewRuntimeAppConfigFence(values)
		if readErr != nil || current.Fingerprint != proof.ConfigFingerprint || current.SecretFence.Fingerprint != proof.SecretFingerprint {
			return empty, ErrConflict
		}
	}
	if err = suspendTransition(ctx, tx, r, expected, next, pause, resume); err != nil {
		return empty, err
	}
	spec := rehearsalresume.Decode(string(member.SuspendResumeSpec))
	return spec, tx.Commit(ctx)
}

var _ rehearsalresume.Store = (*PgStore)(nil)

func suspendOwnerStep(expected, next string) bool {
	return expected == "waiting" && next == "armed" || expected == "claimed" && next == "fenced" || expected == "fenced" && next == "paused" || expected == "paused" && next == "resuming" || expected == "resuming" && next == "resumed"
}

func suspendInventory(raw []byte) ([]coldInstance, error) {
	var live []coldInstance
	err := json.Unmarshal(raw, &live)
	return live, err
}
