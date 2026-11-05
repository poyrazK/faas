// adr: 570, 590
package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s invocationVersionPolicyReader) ResolveInvocationPinScope(ctx context.Context, appID, revision, release string) (string, error) {
	app, rev, rel, err := invocationPinScopeIDs(appID, revision, release)
	if err != nil {
		return "", err
	}
	scope, err := sqlc.New().ReadInvocationPinScope(ctx, s.tx, sqlc.ReadInvocationPinScopeParams{AppID: app, RevisionID: rev, ReleaseID: rel})
	return scope, mapErr(err)
}

func (s invocationVersionPolicyReader) ProjectEnvironmentBySlug(ctx context.Context, account, project, scope string) (ProjectEnvironment, error) {
	data, err := sqlc.New().ReadInvocationVersionEnvironment(ctx, s.tx, sqlc.ReadInvocationVersionEnvironmentParams{
		AccountID: uuidToPgtype(account), ProjectID: uuidToPgtype(project), Scope: scope})
	return decodePublicHostJSON[ProjectEnvironment](data, err)
}

func (s invocationVersionPolicyReader) InvocationEnvironmentID(ctx context.Context, id string) (string, error) {
	row, err := sqlc.New().ReadInvocationEnvironmentOwner(ctx, s.tx, mustPgUUID(id))
	return row.EnvironmentID, mapErr(err)
}

func (s invocationVersionPolicyReader) InvocationWorkEnvironmentAdmission(ctx context.Context, id string) (InvocationWorkEnvironmentAdmission, error) {
	return readInvocationWorkEnvironmentAdmissionDB(ctx, s.tx, id)
}

func (s invocationVersionPolicyReader) InvocationEnvironmentQueueAdmission(ctx context.Context, id string) (InvocationEnvironmentQueueAdmission, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return InvocationEnvironmentQueueAdmission{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadSnapshotInvocationEnvironmentQueueAdmission(ctx, s.tx, mustPgUUID(id))
	if err != nil {
		return InvocationEnvironmentQueueAdmission{}, mapErr(err)
	}
	return InvocationEnvironmentQueueAdmission{InvocationID: pgUUIDString(row.InvocationID), EnvironmentID: pgUUIDString(row.EnvironmentID),
		AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), ConsumerID: pgUUIDString(row.ConsumerID), RuntimeSetID: pgUUIDString(row.RuntimeSetID),
		DeploymentID: pgUUIDString(row.DeploymentID), WorkloadSpecID: pgUUIDString(row.WorkloadSpecID), PinHash: row.PinHash, SettingsHash: row.SettingsHash,
		DefinitionHash: row.DefinitionHash, QueueName: row.QueueName, AdmittedAt: row.AdmittedAt.Time.UTC()}, nil
}

func readSnapshotWorkloadSpec(ctx context.Context, db sqlc.DBTX, account, project, deployment string) (ProjectEnvironmentWorkloadSpec, []byte, error) {
	data, err := sqlc.New().ReadInvocationVersionWorkloadSpec(ctx, db, sqlc.ReadInvocationVersionWorkloadSpecParams{
		AccountID: uuidToPgtype(account), ProjectID: uuidToPgtype(project), DeploymentID: uuidToPgtype(deployment)})
	spec, err := decodePublicHostJSON[ProjectEnvironmentWorkloadSpec](data, err)
	if err != nil {
		return spec, data, err
	}
	hash, err := WorkloadSettingsHash(spec.Settings)
	if err != nil || hash != spec.Hash {
		return ProjectEnvironmentWorkloadSpec{}, data, ErrConflict
	}
	return spec, data, nil
}

func (s invocationVersionPolicyReader) ProjectEnvironmentWorkloadSpecForDeployment(ctx context.Context, account, project, deployment string) (ProjectEnvironmentWorkloadSpec, error) {
	spec, _, err := readSnapshotWorkloadSpec(ctx, s.tx, account, project, deployment)
	return spec, err
}

func (s invocationVersionPolicyReader) ProjectEnvironmentQueueConsumersForDeployment(ctx context.Context, account, project, deployment string) (ProjectEnvironmentQueueRuntimeSet, error) {
	if err := validateQueuePreparationIDs(account, project, deployment); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	dep, err := s.DeploymentByID(ctx, deployment)
	if err != nil || dep.Status != DeployLive || !invocationStageScope(dep.Scope) {
		return ProjectEnvironmentQueueRuntimeSet{}, ErrNotFound
	}
	spec, err := s.ProjectEnvironmentWorkloadSpecForDeployment(ctx, account, project, deployment)
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	book, err := queuePreparationBook(spec)
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	set, err := readSnapshotQueueRuntimeSetDB(ctx, s.tx, spec.EnvironmentSlug, deployment)
	if err != nil {
		return set, err
	}
	if err := validateQueueRuntimeSet(set, spec, deployment, book); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	return set, nil
}

func (s invocationVersionPolicyReader) QueueBindingHistoryByID(ctx context.Context, accountID, appID, id string) (QueueBinding, error) {
	account, app, binding, err := queueBindingIdentity(accountID, appID, id)
	if err != nil {
		return QueueBinding{}, err
	}
	row, err := sqlc.New().QueueBindingHistoryByID(ctx, s.tx, sqlc.QueueBindingHistoryByIDParams{ID: binding, AccountID: account, AppID: app})
	if err != nil {
		return QueueBinding{}, fmt.Errorf("state: snapshot queue binding: %w", mapErr(err))
	}
	return queueConsumerBindingFromSQL(row), nil
}

// The owning MemStore mutex is held for this reader's entire callback.

func (s memInvocationVersionReader) ResolveInvocationPinScope(_ context.Context, appID, revision, release string) (string, error) {
	if _, _, _, err := invocationPinScopeIDs(appID, revision, release); err != nil {
		return "", err
	}
	app, ok := s.store.apps[appID]
	if !ok || app.Status == AppDeleted {
		return "", ErrNotFound
	}
	if revision != "" {
		revision, err := canonicalInvocationPin(revision)
		if err != nil {
			return "", err
		}
		if _, ok := s.store.deployments[revision]; !ok {
			revision = strings.ReplaceAll(revision, "-", "")
		}
		dep, ok := s.store.deployments[revision]
		if !ok || dep.AppID != app.ID {
			return "", ErrNotFound
		}
		return normalizedDeploymentScope(dep.Scope), nil
	}
	release, err := canonicalInvocationPin(release)
	if err != nil {
		return "", err
	}
	set, ok := s.store.projectReleaseSets[release]
	if !ok || app.ProjectID == "" || set.ProjectID != app.ProjectID || set.AccountID != app.AccountID {
		return "", ErrNotFound
	}
	return set.EnvironmentSlug, nil
}

func (s memInvocationVersionReader) ProjectEnvironmentBySlug(_ context.Context, accountID, projectID, slug string) (ProjectEnvironment, error) {
	project, ok := s.store.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ProjectEnvironment{}, ErrNotFound
	}
	for _, env := range s.store.projectEnvironments {
		if env.ProjectID == projectID && env.Slug == slug {
			return env, nil
		}
	}
	return ProjectEnvironment{}, ErrNotFound
}

func (s memInvocationVersionReader) InvocationEnvironmentID(_ context.Context, id string) (string, error) {
	inv, ok := s.store.invocations[id]
	if !ok {
		return "", ErrNotFound
	}
	return inv.EnvironmentID, nil
}

// All scoped writers and claims take this shared environment lock before a
// lane/row lock. Environment deletion takes the exclusive lock first, so it
// observes a complete admission or wins before any queue mutation.

func (s memInvocationVersionReader) InvocationWorkEnvironmentAdmission(_ context.Context, id string) (InvocationWorkEnvironmentAdmission, error) {
	owner, found := s.store.invocationWorkEnvironmentAdmissions[id]
	if !found {
		return owner, ErrNotFound
	}
	return cloneInvocationWorkAdmission(owner), nil
}

func (s memInvocationVersionReader) InvocationEnvironmentQueueAdmission(_ context.Context, id string) (InvocationEnvironmentQueueAdmission, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return InvocationEnvironmentQueueAdmission{}, ErrInvalidArgument
	}
	owner, ok := s.store.invocationEnvironmentQueueAdmissions[id]
	if !ok {
		return owner, ErrNotFound
	}
	return owner, nil
}

func (s memInvocationVersionReader) ProjectEnvironmentWorkloadSpecForDeployment(_ context.Context, accountID, projectID, deploymentID string) (ProjectEnvironmentWorkloadSpec, error) {
	deployment, ok := s.store.deployments[deploymentID]
	spec, found := s.store.projectEnvironmentWorkloadSpecs[s.store.projectEnvironmentWorkloadDeploymentSpecs[deploymentID]]
	if !ok || !found || spec.AccountID != accountID || spec.ProjectID != projectID ||
		spec.AppID != deployment.AppID || spec.EnvironmentSlug != workloadEnvironmentSlug(deployment.Scope) {
		return ProjectEnvironmentWorkloadSpec{}, ErrNotFound
	}
	if _, err := s.store.workloadSpecEnvironmentLocked(accountID, projectID, spec.EnvironmentSlug, spec.AppID); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	return cloneWorkloadSpec(spec)
}

func (s memInvocationVersionReader) QueueBindingHistoryByID(_ context.Context, accountID, appID, id string) (QueueBinding, error) {
	b, ok := s.store.queueBindings[id]
	if !ok || b.AccountID != accountID || b.AppID != appID {
		return QueueBinding{}, ErrNotFound
	}
	return cloneQueueBinding(b), nil
}

func (s memInvocationVersionReader) ProjectEnvironmentQueueConsumersForDeployment(_ context.Context, account, project, deployment string) (ProjectEnvironmentQueueRuntimeSet, error) {
	if err := validateQueuePreparationIDs(account, project, deployment); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	return s.store.projectEnvironmentQueueConsumersLocked(account, project, deployment, false, true)
}
