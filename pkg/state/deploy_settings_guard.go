package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DeploySettingsGuard is only used by API recovery updates. Prepare compares
// targeted fields, validates and builds the patch from the locked current app.
// It must not perform I/O or invoke the store while the lock is held.
type DeploySettingsGuard struct {
	AppID        string
	AccountID    string
	DeploymentID string
	Prepare      func(App) (UpdateAppParams, error)
}

type DeploySettingsConflictError struct {
	Reason string
	Fields []string
}

func (e *DeploySettingsConflictError) Error() string {
	if len(e.Fields) > 0 {
		fields := append([]string(nil), e.Fields...)
		sort.Strings(fields)
		return "targeted app settings changed: " + strings.Join(fields, ", ")
	}
	return "deploy settings precondition failed: " + e.Reason
}

func (e *DeploySettingsConflictError) Unwrap() error { return ErrConflict }

func prepareDeploySettingsUpdate(app App, latestID string, latestStatus DeploymentStatus, p UpdateAppParams) (UpdateAppParams, error) {
	guard := p.DeploySettingsGuard
	if guard.AppID != app.ID || guard.AccountID != app.AccountID || app.Status == AppDeleted {
		return p, &DeploySettingsConflictError{Reason: "app_identity_changed"}
	}
	if latestID != guard.DeploymentID || latestStatus != DeployLive {
		return p, &DeploySettingsConflictError{Reason: "deployment_changed"}
	}
	if guard.Prepare == nil {
		return p, ErrInvalidArgument
	}
	prepared, err := guard.Prepare(app)
	prepared.DeploySettingsGuard = nil
	return prepared, err
}

func prepareDeploySettingsUpdateTx(ctx context.Context, tx pgx.Tx, app App, p UpdateAppParams) (UpdateAppParams, error) {
	if p.DeploySettingsGuard == nil {
		return p, nil
	}
	latest, err := sqlc.New().ReadDeploySettingsLatest(ctx, tx, app.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, fmt.Errorf("state: read guarded deployment: %w", err)
	}
	return prepareDeploySettingsUpdate(app, latest.ID, DeploymentStatus(latest.Status), p)
}

func (m *MemStore) prepareDeploySettingsUpdateLocked(app App, p UpdateAppParams) (UpdateAppParams, error) {
	if p.DeploySettingsGuard == nil {
		return p, nil
	}
	var latest Deployment
	for _, deployment := range m.deployments {
		if deployment.AppID == app.ID && (latest.ID == "" || deployment.CreatedAt.After(latest.CreatedAt) ||
			(deployment.CreatedAt.Equal(latest.CreatedAt) && deployment.ID > latest.ID)) {
			latest = deployment
		}
	}
	return prepareDeploySettingsUpdate(app, latest.ID, latest.Status, p)
}
