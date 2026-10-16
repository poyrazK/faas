package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// FrozenThawBasis is what a frozen VM's thaw guard compares with (ADR-1342),
// read when the VM freezes.
type FrozenThawBasis struct {
	// Selected reports whether ThawFrozenInstance's selection rule holds:
	// the app's active production release pins the deployment, or no release
	// pins the app and the deployment is its only live production-lane
	// deployment with traffic. It also requires an active app without a
	// database cutover barrier.
	Selected bool
	// The app's runtime-config stamp rows: the app row and the scope rows
	// for "default" and the deployment's scope. Nil is a row that does not
	// exist.
	AppStamp, DefaultScopeStamp, DeploymentScopeStamp *time.Time
	// StandardsVersion identifies the app's application-standards
	// enrollment row as read; empty when it has none. The enrollment rule
	// itself is evaluated in Go when the ticket is issued.
	StandardsVersion string
}

// FrozenThaw is a ticketed thaw's guarded publication (ADR-1342): the
// instance, the app, account and deployment as the ticket recorded them, the
// captured config fence and the basis read at freeze.
type FrozenThaw struct {
	AccountID, AppID, InstanceID, NodeID, WakeID string
	OrgID                                        string
	Fence                                        RuntimeAppConfigFence
	AppType                                      AppType
	RAMMB, CPUMillicores, MaxConcurrency         int
	Plan                                         string
	Basis                                        FrozenThawBasis
	Bookkeeping                                  *RuntimePublicationBookkeeping
}

// FrozenThawStore reads a thaw guard's basis at freeze and runs the guarded
// thaw (ADR-1342).
type FrozenThawStore interface {
	FrozenThawBasis(ctx context.Context, accountID, appID, deploymentID string) (FrozenThawBasis, error)
	// ThawFrozenInstance moves the instance FROZEN -> RUNNING when every
	// guard holds, and returns ErrConflict, changing nothing, when one does
	// not.
	ThawFrozenInstance(ctx context.Context, t FrozenThaw) (Instance, error)
}

var (
	_ FrozenThawStore = (*PgStore)(nil)
	_ FrozenThawStore = (*MemStore)(nil)
)

func validateFrozenThaw(t FrozenThaw) error {
	for _, value := range []string{t.AccountID, t.AppID, t.InstanceID, t.NodeID, t.WakeID, t.Fence.SecretFence.DeploymentID} {
		if id, err := uuid.Parse(value); err != nil || id == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	for _, optional := range []string{t.Fence.SecretFence.EnvironmentID, t.OrgID} {
		if _, err := uuid.Parse(optional); optional != "" && err != nil {
			return ErrInvalidArgument
		}
	}
	if t.Fence.SecretFence.empty() || !validRuntimeAppSecretFence(t.Fence.SecretFence) || !validRuntimeAppConfigFence(t.Fence) ||
		t.Plan == "" || !validPublicationBookkeeping(t.Bookkeeping) {
		return ErrInvalidArgument
	}
	return nil
}

func (s *PgStore) FrozenThawBasis(ctx context.Context, accountID, appID, deploymentID string) (FrozenThawBasis, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return FrozenThawBasis{}, err
	}
	row, err := sqlc.New().FrozenThawBasis(ctx, s.pool, sqlc.FrozenThawBasisParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID),
	})
	if err != nil {
		return FrozenThawBasis{}, mapErr(err)
	}
	return FrozenThawBasis{Selected: row.Selected, AppStamp: timestampPtr(row.AppStamp),
		DefaultScopeStamp: timestampPtr(row.DefaultScopeStamp), DeploymentScopeStamp: timestampPtr(row.DeploymentScopeStamp),
		StandardsVersion: row.StandardsVersion}, nil
}

func (s *PgStore) ThawFrozenInstance(ctx context.Context, t FrozenThaw) (Instance, error) {
	if err := validateFrozenThaw(t); err != nil {
		return Instance{}, err
	}
	params := sqlc.ThawFrozenInstanceParams{
		AccountID: mustPgUUID(t.AccountID), AppID: mustPgUUID(t.AppID), InstanceID: mustPgUUID(t.InstanceID),
		NodeID: mustPgUUID(t.NodeID), WakeID: mustPgUUID(t.WakeID), DeploymentID: mustPgUUID(t.Fence.SecretFence.DeploymentID),
		Scope: t.Fence.SecretFence.Scope, AppType: string(t.AppType), RamMb: int32(t.RAMMB),
		CpuMillicores: int32(t.CPUMillicores), MaxConcurrency: int32(t.MaxConcurrency), Plan: t.Plan,
		ConfigFingerprint: t.Fence.Fingerprint,
		AppStamp:          pgTimestamp(t.Basis.AppStamp), DefaultScopeStamp: pgTimestamp(t.Basis.DefaultScopeStamp),
		DeploymentScopeStamp: pgTimestamp(t.Basis.DeploymentScopeStamp), StandardsVersion: t.Basis.StandardsVersion,
	}
	if t.OrgID != "" {
		params.OrgID = mustPgUUID(t.OrgID)
	}
	if t.Fence.SecretFence.EnvironmentID != "" {
		params.EnvironmentID = mustPgUUID(t.Fence.SecretFence.EnvironmentID)
	}
	if b := t.Bookkeeping; b != nil {
		if b.EventKind != "" {
			params.EventKind = pgtype.Text{String: b.EventKind, Valid: true}
			params.EventData = b.EventData
		}
		if b.NotifyChannel != "" {
			params.NotifyChannel = pgtype.Text{String: b.NotifyChannel, Valid: true}
			params.NotifyPayload = pgtype.Text{String: b.NotifyPayload, Valid: true}
		}
	}
	row, err := sqlc.New().ThawFrozenInstance(ctx, s.pool, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrConflict
	}
	if err != nil {
		return Instance{}, fmt.Errorf("state: thaw frozen instance: %w", err)
	}
	return instanceFromRuntimePublication(sqlc.PublishOwnedInstanceRuntimeRow{
		ID: row.ID, AppID: row.AppID, DeploymentID: row.DeploymentID, State: row.State, Netns: row.Netns,
		GuestUid: row.GuestUid, HostIp: row.HostIp, RamMb: row.RamMb, StartedAt: row.StartedAt,
		LastRequestAt: row.LastRequestAt, ParkedAt: row.ParkedAt, NodeID: row.NodeID, WakeID: row.WakeID,
		FrameworkReadyAt: row.FrameworkReadyAt, TailCount: row.TailCount, Mode: row.Mode, RequestCount: row.RequestCount,
	}), nil
}

func timestampPtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time
	return &at
}

func pgTimestamp(at *time.Time) pgtype.Timestamptz {
	if at == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *at, Valid: true}
}

func (m *MemStore) FrozenThawBasis(_ context.Context, accountID, appID, deploymentID string) (FrozenThawBasis, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return FrozenThawBasis{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	dep, depOK := m.deployments[deploymentID]
	if !ok || app.AccountID != accountID || !depOK || dep.AppID != appID {
		return FrozenThawBasis{}, ErrNotFound
	}
	return m.frozenThawBasisLocked(app, dep), nil
}

// frozenThawBasisLocked mirrors FrozenThawBasis's SQL. MemStore has no
// database cutover barrier; its enrollment version is a digest of the row.
func (m *MemStore) frozenThawBasisLocked(app App, dep Deployment) FrozenThawBasis {
	basis := FrozenThawBasis{Selected: app.Status == AppActive && m.frozenThawSelectedLocked(app, dep)}
	for _, row := range m.applicationStandardEnrollments {
		if app.OrgID != "" && sameStandardUUID(row.AppID, app.ID) && sameStandardUUID(row.OrgID, app.OrgID) {
			raw, _ := json.Marshal(row)
			basis.StandardsVersion = fmt.Sprintf("%x", sha256.Sum256(raw))
		}
	}
	if at, ok := m.runtimeConfigChangedAt[app.ID]; ok {
		basis.AppStamp = &at
	}
	if at, ok := m.environmentRuntimeConfigChangedAt[environmentRuntimeKey{AppID: app.ID, Scope: DefaultEnvScope}]; ok {
		basis.DefaultScopeStamp = &at
	}
	if at, ok := m.environmentRuntimeConfigChangedAt[environmentRuntimeKey{AppID: app.ID, Scope: normalizedDeploymentScope(dep.Scope)}]; ok {
		basis.DeploymentScopeStamp = &at
	}
	return basis
}

func frozenThawProductionLane(scope string) bool {
	return scope == "production" || scope == DefaultEnvScope || scope == ""
}

func (m *MemStore) frozenThawSelectedLocked(app App, dep Deployment) bool {
	if dep.Status != DeployLive || dep.TrafficPercent <= 0 || !frozenThawProductionLane(dep.Scope) ||
		dep.ParkedReason == string(ParkReasonSecurityScanRegressed) {
		return false
	}
	pinned := ""
	if app.ProjectID != "" {
		if release, ok := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(app.ProjectID, "production")]]; ok &&
			release.Active && release.AccountID == app.AccountID && release.ProjectID == app.ProjectID {
			pinned = releaseMemberForApp(release, app.ID)
		}
	}
	if pinned != "" {
		return pinned == dep.ID && dep.Scope == "production"
	}
	for _, other := range m.deployments {
		if other.AppID == app.ID && other.ID != dep.ID && other.Status == DeployLive && other.TrafficPercent > 0 && frozenThawProductionLane(other.Scope) {
			return false
		}
	}
	return true
}

func (m *MemStore) ThawFrozenInstance(ctx context.Context, t FrozenThaw) (Instance, error) {
	if err := validateFrozenThaw(t); err != nil {
		return Instance{}, err
	}
	// MemStore has no notification channel to commit a notification with.
	if t.Bookkeeping != nil && t.Bookkeeping.NotifyChannel != "" {
		return Instance{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.frozenThawGuardsHoldLocked(t) {
		return Instance{}, ErrConflict
	}
	instance := m.instances[t.InstanceID]
	instance.State, instance.StartedAt = string(StateRunning), time.Now().UTC()
	if err := m.guardInstanceRuntimeTransitionLocked(ctx, m.instances[t.InstanceID], instance); err != nil {
		return Instance{}, err
	}
	m.instances[t.InstanceID] = instance
	if b := t.Bookkeeping; b != nil && b.EventKind != "" {
		subject := t.InstanceID
		_ = m.appendEventLocked("schedd", b.EventKind, &subject, b.EventData, nil, time.Now())
	}
	return instance, nil
}

func (m *MemStore) frozenThawGuardsHoldLocked(t FrozenThaw) bool {
	instance, ok := m.instances[t.InstanceID]
	proof, proofOK := m.runtimeInstanceConfigProofs[t.InstanceID]
	app, appOK := m.apps[t.AppID]
	acct, acctOK := m.accounts[app.AccountID]
	dep, depOK := m.deployments[t.Fence.SecretFence.DeploymentID]
	if !ok || !proofOK || !appOK || !acctOK || !depOK {
		return false
	}
	if instance.State != string(StateFrozen) || instance.AppID != t.AppID || instance.DeploymentID != dep.ID ||
		instance.NodeID != t.NodeID || instance.WakeID != t.WakeID ||
		proof.WakeID != instance.WakeID || proof.NodeID != instance.NodeID || proof.Fence.Fingerprint != t.Fence.Fingerprint {
		return false
	}
	if app.OrgID != t.OrgID || app.OrgID != "" && !m.frozenThawEnrollmentCurrentLocked(app, t.Basis.StandardsVersion) {
		return false
	}
	if app.AccountID != t.AccountID || app.Type != t.AppType || app.RAMMB != t.RAMMB ||
		app.CPUMillicores != t.CPUMillicores || app.MaxConcurrency != t.MaxConcurrency ||
		!acct.Active() || string(acct.Plan) != t.Plan || normalizedDeploymentScope(dep.Scope) != t.Fence.SecretFence.Scope {
		return false
	}
	now := m.frozenThawBasisLocked(app, dep)
	return now.Selected && sameStamp(now.AppStamp, t.Basis.AppStamp) &&
		sameStamp(now.DefaultScopeStamp, t.Basis.DefaultScopeStamp) && sameStamp(now.DeploymentScopeStamp, t.Basis.DeploymentScopeStamp)
}

// frozenThawEnrollmentCurrentLocked mirrors the guard's enrollment clause:
// the row exists, is the version the ticket read and has no expired
// exception.
func (m *MemStore) frozenThawEnrollmentCurrentLocked(app App, version string) bool {
	for _, row := range m.applicationStandardEnrollments {
		if sameStandardUUID(row.AppID, app.ID) && sameStandardUUID(row.OrgID, app.OrgID) {
			raw, _ := json.Marshal(row)
			return fmt.Sprintf("%x", sha256.Sum256(raw)) == version &&
				(row.ExceptionExpiresAt == nil || time.Now().Before(*row.ExceptionExpiresAt))
		}
	}
	return false
}

func sameStamp(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
