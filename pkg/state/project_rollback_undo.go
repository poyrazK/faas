package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrProjectRollbackUndoPlanChanged = errors.New("reviewed project rollback undo plan changed")

type ProjectRollbackUndoStore interface {
	CreateProjectRollbackUndo(context.Context, string, ProjectRollbackRecord, ProjectRollbackRecord) (ProjectRollbackRecord, error)
	ProjectRollbackUndoServingAt(context.Context, string, api.ProjectRollbackStep) (*time.Time, error)
}

func projectRollbackAutoUndoReady(source ProjectRollbackRecord) bool {
	if source.Status != "running" || source.Phase != api.ProjectRollbackReleasing || source.UndoOf != "" ||
		source.PauseRequestedAt != nil || source.AutoUndo == nil ||
		source.AutoUndo.Status != api.ProjectRollbackAutoUndoReady || source.AutoUndo.TriggerAppID == "" {
		return false
	}
	switch source.AutoUndo.Trigger {
	case "":
		return source.UndoOnHealthFailure
	case api.ProjectRollbackAutoUndoLiveTrafficTrigger:
		return source.VerifyLiveTraffic && source.UndoOnLiveTrafficFailure &&
			len(source.LiveTrafficPolicies[source.AutoUndo.TriggerAppID].Routes) > 0 &&
			slices.ContainsFunc(source.Steps, source.AutoUndo.MatchesLiveTrafficFailure)
	default:
		return false
	}
}

// The inverse must use the exact saved pairs, in reverse cutover order.
func validProjectRollbackUndo(source, undo ProjectRollbackRecord, automatic bool) bool {
	if (!automatic && source.Status != "blocked" || automatic && (!projectRollbackAutoUndoReady(source) || undo.AcknowledgeReleases)) ||
		source.UndoOf != "" || undo.UndoOf != source.ID ||
		undo.ProjectID != source.ProjectID || undo.Phase != api.ProjectRollbackPreparing ||
		undo.Status != "running" || len(undo.Steps) == 0 {
		return false
	}
	i := 0
	for _, from := range slices.Backward(source.Steps) {
		if from.Status != "rolled_back" {
			continue
		}
		if i == len(undo.Steps) {
			return false
		}
		step := undo.Steps[i]
		if step.AppID != from.AppID || step.TargetID != from.CurrentID || step.CurrentID != from.TargetID ||
			step.Status != "planned" || step.CurrentServedAt == nil || step.AttemptID == "" {
			return false
		}
		i++
	}
	return i == len(undo.Steps)
}

func linkProjectRollbackUndo(source *ProjectRollbackRecord, id string, automatic bool) {
	source.UndoOperationID = id
	if automatic {
		now := time.Now().UTC()
		source.Status, source.UpdatedAt = "blocked", &now
		source.AutoUndo.Status = api.ProjectRollbackAutoUndoStarted
	}
}

func checkProjectRollbackUndoReplay(source, saved, requested ProjectRollbackRecord) error {
	if saved.UndoOf != source.ID {
		return ErrConflict
	}
	if requested.PlanHash != "" && (saved.PlanHash != requested.PlanHash || saved.AcknowledgeReleases != requested.AcknowledgeReleases) {
		return ErrProjectRollbackUndoPlanChanged
	}
	if saved.AcknowledgeReleases != requested.AcknowledgeReleases {
		return ErrConflict
	}
	return nil
}

// CreateProjectRollbackUndo links and admits an undo in the same transaction.
// Resuming the original and accepting its undo both lock the source row.
func (s *PgStore) CreateProjectRollbackUndo(ctx context.Context, account string, expected, undo ProjectRollbackRecord) (ProjectRollbackRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return undo, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	source, err := scanProjectRollback(tx.QueryRow(ctx, `
		SELECT o.version, o.next_run_at, o.receipt FROM project_rollback_operations o
		JOIN projects p ON p.id = o.project_id
		WHERE o.id = $1 AND p.id = $2 AND p.account_id = $3 FOR UPDATE OF o`, expected.ID, expected.ProjectID, account))
	if err != nil {
		return undo, err
	}
	if source.UndoOperationID != "" {
		saved, err := scanProjectRollback(tx.QueryRow(ctx, `
			SELECT version, next_run_at, receipt FROM project_rollback_operations
			WHERE id = $1 AND project_id = $2`, source.UndoOperationID, source.ProjectID))
		if err == nil {
			err = checkProjectRollbackUndoReplay(source, saved, undo)
		}
		return saved, err
	}
	automatic := projectRollbackAutoUndoReady(expected)
	if source.Version != expected.Version || !validProjectRollbackUndo(source, undo, automatic) {
		return undo, ErrConflict
	}
	if undo.PlanHash != "" {
		for i, restored := range projectRollbackUndoCutovers(source) {
			if err := checkPgProjectRollbackUndoStep(ctx, tx, source, restored, undo.Steps[i], undo.ReleaseReviews[restored.AppID]); err != nil {
				return undo, err
			}
		}
	}
	linkProjectRollbackUndo(&source, undo.ID, automatic)
	fields := map[string]any{"undo_operation_id": source.UndoOperationID}
	if automatic {
		fields["status"], fields["updated_at"], fields["auto_undo"] = source.Status, source.UpdatedAt, source.AutoUndo
	}
	receipt, err := json.Marshal(fields)
	if err != nil {
		return undo, err
	}
	// Retire the original running slot before inserting the inverse. The
	// source link, finished cleanup, and inverse admission commit together.
	tag, err := tx.Exec(ctx, `
		UPDATE project_rollback_operations SET status = 'blocked', version = version + 1, receipt = receipt || $2::jsonb
		WHERE id = $1 AND (NOT $3::boolean OR next_run_at > clock_timestamp())`, source.ID, receipt, automatic)
	if err != nil {
		return undo, err
	}
	if tag.RowsAffected() != 1 {
		return undo, ErrConflict
	}
	if err := createProjectRollback(ctx, tx, account, undo); err != nil {
		return undo, err
	}
	if err := continueProjectRollbackUndoArtifactHoldsTx(ctx, tx, source.ID, undo.ID); err != nil {
		return undo, err
	}
	return undo, tx.Commit(ctx)
}

func (m *MemStore) CreateProjectRollbackUndo(ctx context.Context, account string, expected, undo ProjectRollbackRecord) (ProjectRollbackRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return undo, err
	}
	source, ok := m.projectRollbackOperations[expected.ID]
	if !ok || source.ProjectID != expected.ProjectID || m.projects[source.ProjectID].AccountID != account {
		return undo, ErrNotFound
	}
	if source.UndoOperationID != "" {
		saved, ok := m.projectRollbackOperations[source.UndoOperationID]
		if !ok {
			return undo, ErrConflict
		}
		if err := checkProjectRollbackUndoReplay(source, saved, undo); err != nil {
			return undo, err
		}
		return cloneProjectRollback(saved), nil
	}
	automatic := projectRollbackAutoUndoReady(expected)
	if _, exists := m.projectRollbackOperations[undo.ID]; exists || source.Version != expected.Version ||
		!validProjectRollbackUndo(source, undo, automatic) ||
		(automatic && !source.nextRun.After(time.Now()) || !automatic && m.projectRollbackRunningLocked(source.ProjectID)) {
		return undo, ErrConflict
	}
	if undo.PlanHash != "" {
		for i, restored := range projectRollbackUndoCutovers(source) {
			step := undo.Steps[i]
			app := m.apps[step.AppID]
			if app.ProjectID != source.ProjectID || app.Status == AppDeleted {
				return undo, ErrProjectRollbackUndoPlanChanged
			}
			servedAt, err := m.projectRollbackUndoServingAtLocked(source.ID, restored)
			if err != nil || !matchesProjectRollbackUndoServing(step, servedAt, m.liveDeploymentsLocked(step.AppID)) {
				return undo, ErrProjectRollbackUndoPlanChanged
			}
			target := m.deployments[step.TargetID]
			runs := m.releaseRunsCrossedByLocked(step.AppID, step.TargetID)
			undone := m.releasesUndoneByDatabaseSwitchesLocked(step.AppID, target.Scope)
			if target.AppID != step.AppID || target.DeletedAt != nil ||
				!matchesProjectRollbackUndoReleases(step.TargetID, runs, undone, undo.ReleaseReviews[step.AppID]) {
				return undo, ErrProjectRollbackUndoPlanChanged
			}
		}
	}
	source.Version++
	linkProjectRollbackUndo(&source, undo.ID, automatic)
	undo.nextRun = time.Now()
	m.projectRollbackOperations[source.ID] = source
	m.projectRollbackOperations[undo.ID] = cloneProjectRollback(undo)
	if automatic {
		m.finishProjectRollbackUndoArtifactHoldsLocked(source.ID, source.Status, undo.nextRun)
	}
	m.continueProjectRollbackUndoArtifactHoldsLocked(source.ID, undo.ID, undo.nextRun)
	return cloneProjectRollback(undo), nil
}

func projectRollbackUndoCutovers(source ProjectRollbackRecord) []api.ProjectRollbackStep {
	var cutovers []api.ProjectRollbackStep
	for _, step := range slices.Backward(source.Steps) {
		if step.Status == "rolled_back" {
			cutovers = append(cutovers, step)
		}
	}
	return cutovers
}

func checkPgProjectRollbackUndoStep(ctx context.Context, tx pgx.Tx, source ProjectRollbackRecord, restored, step api.ProjectRollbackStep, reviewed []ReleaseRun) error {
	// Release starts and traffic writes take the app lock. A release already
	// running can finish independently, so hold its task row while comparing.
	var targetScope string
	err := tx.QueryRow(ctx, `
		SELECT d.scope FROM apps a JOIN deployments d ON d.app_id = a.id AND d.id = $3
		WHERE a.id = $1 AND a.project_id = $2 AND a.status <> 'deleted' AND d.deleted_at IS NULL
		FOR UPDATE OF a NOWAIT`, step.AppID, source.ProjectID, step.TargetID).Scan(&targetScope)
	scope := normalizedDeploymentScope(targetScope)
	if err == nil {
		err = lockProjectRollbackReviewedReleaseHistory(ctx, tx, step.AppID, scope)
	}
	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrProjectRollbackUndoPlanChanged
		case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable:
			return ErrConflict
		default:
			return err
		}
	}
	servedAt, err := readProjectRollbackUndoServingAt(ctx, tx, source.ID, restored)
	if errors.Is(err, ErrNotFound) {
		return ErrProjectRollbackUndoPlanChanged
	}
	if err != nil {
		return err
	}
	live, err := readLiveDeployments(ctx, tx, step.AppID)
	if err != nil {
		return err
	}
	if !matchesProjectRollbackUndoServing(step, servedAt, live) {
		return ErrProjectRollbackUndoPlanChanged
	}
	matches, err := pgProjectRollbackReviewedReleasesMatch(ctx, tx, step.AppID, step.TargetID, scope, reviewed)
	if err != nil {
		return err
	}
	if !matches {
		return ErrProjectRollbackUndoPlanChanged
	}
	return nil
}

func matchesProjectRollbackUndoServing(step api.ProjectRollbackStep, servedAt *time.Time, live []Deployment) bool {
	return servedAt.Equal(*step.CurrentServedAt) && matchesProjectRollbackReviewedServing(step.Scope, step.CurrentID, servedAt, live)
}

func matchesProjectRollbackUndoReleases(target string, runs []ReleaseRun, undone []string, reviewed []ReleaseRun) bool {
	if slices.Contains(undone, target) {
		return false
	}
	runs = slices.DeleteFunc(runs, func(run ReleaseRun) bool { return slices.Contains(undone, run.DeploymentID) })
	if len(runs) != len(reviewed) {
		return false
	}
	byDeployment := make(map[string]ReleaseRun, len(reviewed))
	for _, run := range reviewed {
		byDeployment[run.DeploymentID] = run
	}
	for _, run := range runs {
		want, ok := byDeployment[run.DeploymentID]
		if !ok || run.Revision != want.Revision || run.Status != want.Status || !run.StartedAt.Equal(want.StartedAt) {
			return false
		}
	}
	return true
}

// Request-mode cutovers record the exact serving timestamp in the HTTP route
// ledger. Other non-service cutovers write their matching audit event in the
// same locked transaction. A later serving period falls after that audit.
// The service handoff stores nanoseconds in JSON and microseconds in the
// deployment column. Match the checked handoff and allow its rounding delta.
func (s *PgStore) ProjectRollbackUndoServingAt(ctx context.Context, source string, step api.ProjectRollbackStep) (*time.Time, error) {
	return readProjectRollbackUndoServingAt(ctx, s.pool, source, step)
}

func readProjectRollbackUndoServingAt(ctx context.Context, db sqlc.DBTX, source string, step api.ProjectRollbackStep) (*time.Time, error) {
	var servedAt time.Time
	err := db.QueryRow(ctx, `
		SELECT d.rollout_completed_at
		FROM deployment_rollback_operations child
		JOIN deployments d ON d.id = child.target_deployment_id AND d.app_id = child.app_id
		LEFT JOIN project_rollback_http_recovery r ON r.rollback_id = child.id AND r.operation_id = $1
		LEFT JOIN deployment_audit audit ON audit.id = (child.receipt->>'audit_id')::bigint
		  AND audit.deployment_id = d.id AND audit.kind = 'deploy.rolled_back' AND audit.data->>'request_id' = child.id::text
		WHERE child.id = $2 AND child.app_id = $3 AND child.current_deployment_id = $4 AND d.id = $5
		  AND (child.project_rollback_id IS NULL OR child.project_rollback_id = $1)
		  AND child.status = 'complete' AND d.deleted_at IS NULL AND d.status = 'live'
		  AND d.traffic_percent = 100 AND d.rollout_state = 'complete' AND d.rollout_completed_at IS NOT NULL
		  AND CASE WHEN (child.receipt->>'service')::boolean THEN
		            d.service_rollout_handoff #>> '{bindings_check,request_id}' = child.id::text
		            AND d.service_rollout_handoff #>> '{bindings_check,audit_id}' = child.receipt->>'audit_id'
		            AND abs(extract(epoch FROM d.rollout_completed_at -
		              (d.service_rollout_handoff->>'completed_at')::timestamptz)) <= 0.000001
		           WHEN r.routing_committed_at IS NOT NULL THEN d.rollout_completed_at = r.routing_committed_at
		           ELSE d.rollout_completed_at <= audit.at END`,
		source, step.ReceiptID, step.AppID, step.CurrentID, step.TargetID).Scan(&servedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &servedAt, nil
}

func (m *MemStore) ProjectRollbackUndoServingAt(_ context.Context, source string, step api.ProjectRollbackStep) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectRollbackUndoServingAtLocked(source, step)
}

func (m *MemStore) projectRollbackUndoServingAtLocked(source string, step api.ProjectRollbackStep) (*time.Time, error) {
	child, ok := m.checkedRollbacks[step.ReceiptID]
	d := m.deployments[step.TargetID]
	if !ok || child.Status != "complete" || child.AppID != step.AppID || d.AppID != step.AppID ||
		child.TargetDeploymentID != step.TargetID || child.CurrentDeploymentID != step.CurrentID ||
		child.ProjectRollbackID != "" && child.ProjectRollbackID != source ||
		d.Status != DeployLive || d.TrafficPercent != 100 || d.RolloutState != "complete" || d.DeletedAt != nil ||
		d.RolloutCompletedAt == nil || !sameProjectServingPeriod(d.RolloutCompletedAt, child.CompletedAt) {
		return nil, ErrNotFound
	}
	stamp := *d.RolloutCompletedAt
	return &stamp, nil
}

var _ ProjectRollbackUndoStore = (*PgStore)(nil)
var _ ProjectRollbackUndoStore = (*MemStore)(nil)
