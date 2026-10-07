package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) operationPinsLocked(op Operation) {
	if m.operationCodePins == nil {
		m.operationCodePins = map[string]time.Time{}
	}
	if m.operationCodePins[op.DeploymentID].Before(op.ExpiresAt) {
		m.operationCodePins[op.DeploymentID] = op.ExpiresAt
	}
	def, retained := m.operationRetainedDefinitionLocked(op, time.Now())
	release, exists := m.projectReleaseSets[op.ReleaseID]
	if !retained || !exists || !m.operationOwnsReleaseLocked(op, def, release) {
		return
	}
	for _, member := range release.Members {
		app, appExists := m.apps[member.AppID]
		dep, depExists := m.deployments[member.DeploymentID]
		if appExists && depExists && app.Status != AppDeleted && app.AccountID == op.AccountID && app.ProjectID == release.ProjectID &&
			dep.AppID == app.ID && dep.Scope == release.EnvironmentSlug && m.operationCodePins[dep.ID].Before(op.ExpiresAt) {
			m.operationCodePins[dep.ID] = op.ExpiresAt
		}
	}
}

func (m *MemStore) deploymentRevisionRetainedLocked(id string) bool {
	return m.revisionPins[id].After(time.Now()) || m.durableWorkRetainsDeploymentLocked(id)
}

func (m *MemStore) operationRetainedDefinitionLocked(op Operation, now time.Time) (OperationDefinition, bool) {
	def, exists := m.operationData.definitions[op.DefinitionID]
	app, appExists := m.apps[op.AppID]
	dep, depExists := m.deployments[def.DeploymentID]
	return def, exists && appExists && app.Status != AppDeleted && app.AccountID == op.AccountID &&
		def.AccountID == op.AccountID && def.AppID == op.AppID && def.Scope == op.Scope &&
		def.DeploymentID == op.DeploymentID && depExists && dep.AppID == def.AppID && dep.Scope == def.Scope && operationRetained(op, now)
}

func (m *MemStore) operationOwnsReleaseLocked(op Operation, def OperationDefinition, release ProjectReleaseSet) bool {
	app := m.apps[op.AppID]
	return op.ReleaseID == release.ID && release.AccountID == op.AccountID && release.ProjectID == app.ProjectID &&
		release.EnvironmentSlug == def.Scope && releaseMemberForApp(release, def.AppID) == def.DeploymentID
}

func (m *MemStore) operationRetainsReleaseLocked(release ProjectReleaseSet) bool {
	if m.operationData == nil {
		return false
	}
	now := time.Now()
	for _, op := range m.operationData.operations {
		def, retained := m.operationRetainedDefinitionLocked(op, now)
		if retained && m.operationOwnsReleaseLocked(op, def, release) {
			return true
		}
	}
	return false
}

func (m *MemStore) operationRetainsDeploymentLocked(id string) bool {
	if m.operationData == nil {
		return false
	}
	dep, exists := m.deployments[id]
	app, appExists := m.apps[dep.AppID]
	if !exists || !appExists || app.Status == AppDeleted {
		return false
	}
	now := time.Now()
	for _, op := range m.operationData.operations {
		def, retained := m.operationRetainedDefinitionLocked(op, now)
		if !retained {
			continue
		}
		if def.DeploymentID == id {
			return true
		}
		release, releaseExists := m.projectReleaseSets[op.ReleaseID]
		if releaseExists && m.operationOwnsReleaseLocked(op, def, release) && app.AccountID == op.AccountID &&
			app.ProjectID == release.ProjectID && dep.Scope == release.EnvironmentSlug && releaseMemberForApp(release, dep.AppID) == id {
			return true
		}
	}
	return false
}

func retireLiveDeploymentSiblingsTx(ctx context.Context, tx pgx.Tx, appID, scope, deploymentID string) error {
	app, err := operationUUID(appID)
	if err != nil {
		return err
	}
	deployment, err := operationUUID(deploymentID)
	if err != nil {
		return err
	}
	_, err = sqlc.New().RetireLiveDeploymentSiblings(ctx, tx, sqlc.RetireLiveDeploymentSiblingsParams{
		AppID: app, Scope: normalizedDeploymentScope(scope), DeploymentID: deployment})
	return err
}

func operationPinsTx(ctx context.Context, tx pgx.Tx, op Operation) error {
	q := sqlc.New()
	dep, err := operationUUID(op.DeploymentID)
	if err != nil {
		return err
	}
	app, err := operationUUID(op.AppID)
	if err != nil {
		return err
	}
	account, err := operationUUID(op.AccountID)
	if err != nil {
		return err
	}
	expires := pgtype.Timestamptz{Time: op.ExpiresAt, Valid: true}
	var count int64
	if op.ReleaseID != "" {
		release, parseErr := operationUUID(op.ReleaseID)
		if parseErr != nil {
			return parseErr
		}
		// One ordered insert covers the source and every release member. Do
		// not lock a source pin first: two operations on different members
		// would otherwise acquire the same graph's pins in opposite orders.
		count, err = q.PinCustomerOperationReleaseMembers(ctx, tx, sqlc.PinCustomerOperationReleaseMembersParams{
			DeploymentID: dep, AppID: app, AccountID: account, ReleaseID: release, Scope: op.Scope,
			ExpiresAt: expires, MemberLimit: api.ProjectReleaseSetMaxMembers})
	} else {
		count, err = q.PinCustomerOperationDeployment(ctx, tx, sqlc.PinCustomerOperationDeploymentParams{
			DeploymentID: dep, AppID: app, AccountID: account, Scope: op.Scope, ExpiresAt: expires})
	}
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrConflict
	}
	return nil
}

// Lock code before execution/operation rows. The whole release graph must be
// usable after app locks are acquired; a source-only lock cannot serialize
// admission with retirement of another workload in that graph.
func lockOperationCodeTx(ctx context.Context, tx pgx.Tx, op Operation) error {
	account, err := operationUUID(op.AccountID)
	if err != nil {
		return err
	}
	app, err := operationUUID(op.AppID)
	if err != nil {
		return err
	}
	deployment, err := operationUUID(op.DeploymentID)
	if err != nil {
		return err
	}
	q := sqlc.New()
	if op.ReleaseID != "" {
		if err := lockOperationReleaseCodeTx(ctx, tx, op, account, app, deployment); err != nil {
			return err
		}
	} else if _, err := q.LockCustomerOperationCodeApp(ctx, tx, sqlc.LockCustomerOperationCodeAppParams{AppID: app, AccountID: account}); err != nil {
		return mapErr(err)
	}
	status, err := q.LockCustomerOperationDeployment(ctx, tx, sqlc.LockCustomerOperationDeploymentParams{
		DeploymentID: deployment, AppID: app, AccountID: account, Scope: op.Scope})
	if err != nil {
		return mapErr(err)
	}
	if status != string(DeployLive) {
		return ErrConflict
	}
	return nil
}

func lockOperationReleaseCodeTx(ctx context.Context, tx pgx.Tx, op Operation, account, app, deployment pgtype.UUID) error {
	release, err := operationUUID(op.ReleaseID)
	if err != nil {
		return err
	}
	q := sqlc.New()
	bound := int32(api.ProjectReleaseSetMaxMembers + 1)
	count, err := q.CustomerOperationReleaseMemberCount(ctx, tx, sqlc.CustomerOperationReleaseMemberCountParams{
		AccountID: account, AppID: app, DeploymentID: deployment, ReleaseID: release, Scope: op.Scope, MemberLimit: bound})
	if err != nil {
		return err
	}
	if count < 1 || count > api.ProjectReleaseSetMaxMembers {
		return ErrConflict
	}
	apps, err := q.LockCustomerOperationReleaseApps(ctx, tx, sqlc.LockCustomerOperationReleaseAppsParams{
		AccountID: account, ReleaseID: release, MemberLimit: bound})
	if err != nil {
		return err
	}
	if int64(len(apps)) != count {
		return ErrConflict
	}
	deployments, err := q.LockCustomerOperationReleaseDeployments(ctx, tx, sqlc.LockCustomerOperationReleaseDeploymentsParams{
		AccountID: account, ReleaseID: release, MemberLimit: bound})
	if err != nil {
		return err
	}
	if int64(len(deployments)) != count {
		return ErrConflict
	}
	selected, err := q.ResolveRetainedProjectRelease(ctx, tx, sqlc.ResolveRetainedProjectReleaseParams{AppID: app, ReleaseID: release, Scope: op.Scope})
	if err != nil {
		return mapErr(err)
	}
	if selected.DeploymentID != op.DeploymentID {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) operationCodeAvailableLocked(op Operation) bool {
	dep, exists := m.deployments[op.DeploymentID]
	app, appExists := m.apps[op.AppID]
	if !exists || !appExists || app.Status == AppDeleted || app.AccountID != op.AccountID ||
		dep.AppID != op.AppID || dep.Scope != op.Scope || dep.Status != DeployLive {
		return false
	}
	if op.ReleaseID == "" {
		return true
	}
	release, exists := m.projectReleaseSets[op.ReleaseID]
	if !exists || release.AccountID != op.AccountID || release.ProjectID != app.ProjectID || release.EnvironmentSlug != op.Scope ||
		releaseMemberForApp(release, op.AppID) != op.DeploymentID ||
		!m.releaseUsableLocked(release) || len(release.Members) < 1 || len(release.Members) > api.ProjectReleaseSetMaxMembers {
		return false
	}
	for _, member := range release.Members {
		memberApp, exists := m.apps[member.AppID]
		memberDep, depExists := m.deployments[member.DeploymentID]
		if !exists || !depExists || memberApp.Status == AppDeleted || memberApp.AccountID != op.AccountID ||
			memberApp.ProjectID != release.ProjectID || memberDep.AppID != memberApp.ID || memberDep.Scope != op.Scope || memberDep.Status != DeployLive {
			return false
		}
	}
	return true
}
