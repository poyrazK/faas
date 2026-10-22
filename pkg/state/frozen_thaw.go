package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// FrozenThawBasis is what a frozen VM's thaw guard compares with (ADR-1740,
// ADR-1741), read first when the VM freezes.
type FrozenThawBasis struct {
	// Generation is the app's runtime generation. Any change the ordinary
	// wake path would read moves it, so the guarded thaw requires it
	// unchanged.
	Generation int64
	// Selected reports whether the thaw a ticket would allow is the one the
	// ordinary wake path would choose: the app's active production release
	// pins the deployment, or no release pins the app and the deployment is
	// its only live production-lane deployment with traffic. It also
	// requires an active app without a database cutover barrier.
	Selected bool
}

// FrozenThaw is a ticketed thaw's guarded publication (ADR-1740): the
// instance, the config fence its proof captured and the generation the
// ticket read.
type FrozenThaw struct {
	AccountID, AppID, InstanceID, NodeID, WakeID string
	Fence                                        RuntimeAppConfigFence
	Generation                                   int64
	Bookkeeping                                  *RuntimePublicationBookkeeping
}

// FrozenThawStore reads a thaw guard's basis at freeze and runs the guarded
// thaw (ADR-1740).
type FrozenThawStore interface {
	FrozenThawBasis(ctx context.Context, appID, deploymentID string) (FrozenThawBasis, error)
	// ThawFrozenInstance moves the instance FROZEN -> RUNNING when the app's
	// runtime generation is still t.Generation and the proof is unchanged,
	// and returns ErrConflict, changing nothing, otherwise.
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
	if t.Fence.SecretFence.empty() || !validRuntimeAppSecretFence(t.Fence.SecretFence) || !validRuntimeAppConfigFence(t.Fence) ||
		t.Generation <= 0 || !validPublicationBookkeeping(t.Bookkeeping) {
		return ErrInvalidArgument
	}
	return nil
}

func (s *PgStore) FrozenThawBasis(ctx context.Context, appID, deploymentID string) (FrozenThawBasis, error) {
	for _, value := range []string{appID, deploymentID} {
		if _, err := uuid.Parse(value); err != nil {
			return FrozenThawBasis{}, ErrInvalidArgument
		}
	}
	row, err := sqlc.New().FrozenThawBasis(ctx, s.pool, sqlc.FrozenThawBasisParams{AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID)})
	if err != nil {
		return FrozenThawBasis{}, mapErr(err)
	}
	return FrozenThawBasis{Generation: row.Generation, Selected: row.Selected}, nil
}

func (s *PgStore) ThawFrozenInstance(ctx context.Context, t FrozenThaw) (Instance, error) {
	if err := validateFrozenThaw(t); err != nil {
		return Instance{}, err
	}
	params := sqlc.ThawFrozenInstanceParams{
		AppID: mustPgUUID(t.AppID), InstanceID: mustPgUUID(t.InstanceID), NodeID: mustPgUUID(t.NodeID), WakeID: mustPgUUID(t.WakeID),
		DeploymentID: mustPgUUID(t.Fence.SecretFence.DeploymentID), ConfigFingerprint: t.Fence.Fingerprint, Generation: t.Generation,
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

func (m *MemStore) FrozenThawBasis(ctx context.Context, appID, deploymentID string) (FrozenThawBasis, error) {
	generation, err := m.AppRuntimeGeneration(ctx, appID)
	if err != nil {
		return FrozenThawBasis{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	dep, depOK := m.deployments[deploymentID]
	if !ok || !depOK || dep.AppID != appID {
		return FrozenThawBasis{}, ErrNotFound
	}
	return FrozenThawBasis{Generation: generation, Selected: app.Status == AppActive && m.frozenThawSelectedLocked(app, dep)}, nil
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
	generation, err := m.AppRuntimeGeneration(ctx, t.AppID)
	if err != nil || generation != t.Generation {
		return Instance{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.frozenThawGuardsHoldLocked(t) {
		return Instance{}, ErrConflict
	}
	instance := m.instances[t.InstanceID]
	instance.State, instance.StartedAt = string(StateRunning), time.Now().UTC()
	instance.LastRequestAt = instance.StartedAt
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

// frozenThawGuardsHoldLocked mirrors the guards ThawFrozenInstance keeps
// beside the generation: the instance row, its proof and an unexpired
// enrollment exception.
func (m *MemStore) frozenThawGuardsHoldLocked(t FrozenThaw) bool {
	instance, ok := m.instances[t.InstanceID]
	proof, proofOK := m.runtimeInstanceConfigProofs[t.InstanceID]
	app, appOK := m.apps[t.AppID]
	if !ok || !proofOK || !appOK || instance.State != string(StateFrozen) || instance.AppID != t.AppID ||
		instance.DeploymentID != t.Fence.SecretFence.DeploymentID || instance.NodeID != t.NodeID || instance.WakeID != t.WakeID ||
		proof.WakeID != instance.WakeID || proof.NodeID != instance.NodeID || proof.Fence.Fingerprint != t.Fence.Fingerprint {
		return false
	}
	if app.OrgID == "" {
		return true
	}
	for _, row := range m.applicationStandardEnrollments {
		if sameStandardUUID(row.AppID, app.ID) && sameStandardUUID(row.OrgID, app.OrgID) {
			return row.ExceptionExpiresAt == nil || time.Now().Before(*row.ExceptionExpiresAt)
		}
	}
	return false
}
