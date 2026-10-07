package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrWorkflowDeploymentUnavailable never permits fallback to current code.
var ErrWorkflowDeploymentUnavailable = fmt.Errorf("workflow deployment is unavailable: %w", ErrConflict)

func mapWorkflowCodePinError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == "workflow_code_available" {
		return ErrWorkflowDeploymentUnavailable
	}
	return err
}

func resolveWorkflowInvocationVersion(ctx context.Context, store invocationAppReader, app App, inv Invocation, revision, release string) (Invocation, InvocationVersion, bool, error) {
	reader, ok := store.(interface {
		GetWorkflowRun(context.Context, string) (*WorkflowRun, error)
		DeploymentByID(context.Context, string) (Deployment, error)
	})
	if !ok || inv.Source != "workflow" {
		return inv, InvocationVersion{}, true, ErrWorkflowDeploymentUnavailable
	}
	run, err := reader.GetWorkflowRun(ctx, inv.WorkflowRunID)
	if err != nil {
		return inv, InvocationVersion{}, true, err
	}
	if app.Status == AppDeleted || run.AppID != app.ID || run.PlatformTenantID != inv.PlatformTenantID || run.Status != WorkflowRunStatusRunning {
		return inv, InvocationVersion{}, true, ErrWorkflowDeploymentUnavailable
	}
	// Pre-migration runs keep their original best-effort routing contract.
	if run.DeploymentID == "" {
		return inv, InvocationVersion{}, false, nil
	}
	dep, err := reader.DeploymentByID(ctx, run.DeploymentID)
	if err != nil {
		return inv, InvocationVersion{}, true, err
	}
	if dep.AppID != app.ID || dep.Status != DeployLive || dep.DeletedAt != nil || (revision != "" && revision != dep.ID) || release != "" ||
		(inv.DeploymentScope != "" && !invocationScopesMatch(app.ProjectID != "", inv.DeploymentScope, dep.Scope)) {
		return inv, InvocationVersion{}, true, ErrWorkflowDeploymentUnavailable
	}
	inv.DeploymentScope = normalizedDeploymentScope(dep.Scope)
	return inv, InvocationVersion{DeploymentID: dep.ID, Scope: inv.DeploymentScope}, true, nil
}

func (m *MemStore) pinWorkflowDeploymentLocked(appID, deploymentID string) error {
	if deploymentID == "" {
		return nil
	}
	app, owned := m.apps[appID]
	dep, exists := m.deployments[deploymentID]
	if !owned || app.Status == AppDeleted || !exists || dep.AppID != appID || dep.Status != DeployLive || dep.DeletedAt != nil {
		return ErrWorkflowDeploymentUnavailable
	}
	if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(dep)); err != nil {
		return err
	}
	if m.workflowCodePins == nil {
		m.workflowCodePins = map[string]time.Time{}
	}
	// References own the lifetime. The expired receipt lets ordinary cleanup
	// retire code once the last retained run/event is removed.
	m.workflowCodePins[deploymentID] = time.Now().UTC()
	return nil
}

func (m *MemStore) workflowRetainsDeploymentLocked(id string) bool {
	dep, exists := m.deployments[id]
	app, owned := m.apps[dep.AppID]
	if !exists || !owned || app.Status == AppDeleted {
		return false
	}
	for _, run := range m.workflowRuns {
		if run.DeploymentID == id && run.AppID == app.ID {
			return true
		}
	}
	for _, work := range m.eventFanout {
		for _, recipient := range work.RecipientSnapshot {
			if len(recipient.Workflow) != 0 && recipient.DeploymentID == id && recipient.AppID == app.ID && sameMemUUID(recipient.AccountID, app.AccountID) {
				return true
			}
		}
	}
	return false
}

func (m *MemStore) durableWorkRetainsDeploymentLocked(id string) bool {
	return m.operationRetainsDeploymentLocked(id) || m.workflowRetainsDeploymentLocked(id)
}

func (m *MemStore) pinWorkflowEventRecipientsLocked(recipients []PublishedEventRecipient) error {
	for _, recipient := range recipients {
		if len(recipient.Workflow) != 0 {
			if err := m.pinWorkflowDeploymentLocked(recipient.AppID, recipient.DeploymentID); err != nil {
				return err
			}
		}
	}
	return nil
}
