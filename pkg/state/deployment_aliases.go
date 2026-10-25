package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// DeploymentAlias is a stable, customer-chosen name for one immutable
// deployment revision. It does not change production traffic; callers use it
// to resolve a revision independently from whichever deployment is latest.
type DeploymentAlias struct {
	AppID        string
	Name         string
	DeploymentID string
	Revision     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// DeploymentAliasStore is implemented by PgStore and MemStore. It is kept
// separate from Store so unrelated Store fakes do not have to grow with this
// customer-facing API surface.
type DeploymentAliasStore interface {
	ListDeploymentAliases(ctx context.Context, appID string) ([]DeploymentAlias, error)
	SetDeploymentAlias(ctx context.Context, appID, name, deploymentID string) (DeploymentAlias, error)
	DeleteDeploymentAlias(ctx context.Context, appID, name string) error
}

// DeploymentAliasRoutingStore resolves the public one-label alias hostname
// to its app-scoped immutable deployment mapping. It stays separate from
// DeploymentAliasStore so API-only adapters do not need gateway routing reads.
type DeploymentAliasRoutingStore interface {
	DeploymentAliasByHostLabel(ctx context.Context, hostLabel string) (DeploymentAlias, error)
}

var _ DeploymentAliasStore = (*MemStore)(nil)
var _ DeploymentAliasRoutingStore = (*MemStore)(nil)

func deploymentAliasKey(appID, name string) string { return appID + "\x00" + name }

func (m *MemStore) ListDeploymentAliases(_ context.Context, appID string) ([]DeploymentAlias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	aliases := make([]DeploymentAlias, 0)
	for _, alias := range m.deploymentAliases {
		if alias.AppID != appID {
			continue
		}
		if deployment, ok := m.deployments[alias.DeploymentID]; ok {
			alias.Revision = deployment.Revision
			aliases = append(aliases, alias)
		}
	}
	sort.Slice(aliases, func(i, j int) bool { return aliases[i].Name < aliases[j].Name })
	return aliases, nil
}

func (m *MemStore) SetDeploymentAlias(ctx context.Context, appID, name, deploymentID string) (DeploymentAlias, error) {
	if !api.ValidDeploymentAliasName(name) {
		return DeploymentAlias{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted || app.DeletedAt != nil {
		return DeploymentAlias{}, ErrNotFound
	}
	hostLabel, ok := api.DeploymentAliasHostLabel(app.ID, name)
	if !ok {
		return DeploymentAlias{}, ErrInvalidArgument
	}
	for _, candidate := range m.apps {
		if candidate.Slug == hostLabel {
			return DeploymentAlias{}, ErrConflict
		}
	}
	deployment, ok := m.deployments[deploymentID]
	if !ok || deployment.AppID != appID || deployment.DeletedAt != nil || !deployment.DeploymentPreviewActive() {
		return DeploymentAlias{}, ErrNotFound
	}
	if err := m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(deployment)); err != nil {
		return DeploymentAlias{}, err
	}
	now := time.Now().UTC()
	key := deploymentAliasKey(appID, name)
	alias, exists := m.deploymentAliases[key]
	if !exists {
		alias = DeploymentAlias{AppID: appID, Name: name, CreatedAt: now}
	}
	alias.DeploymentID = deploymentID
	alias.Revision = deployment.Revision
	alias.UpdatedAt = now
	if err := appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, app.AccountID, nil, appID, memTrafficPolicyChange{Aliases: map[string]DeploymentAlias{key: alias}})); err != nil {
		return DeploymentAlias{}, err
	}
	m.deploymentAliases[key] = alias
	return alias, nil
}

func (m *MemStore) DeleteDeploymentAlias(ctx context.Context, appID, name string) error {
	if !api.ValidDeploymentAliasName(name) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := deploymentAliasKey(appID, name)
	if _, ok := m.deploymentAliases[key]; !ok {
		return ErrNotFound
	}
	app, found := m.apps[appID]
	if !found {
		return ErrNotFound
	}
	if err := appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, app.AccountID, nil, appID, memTrafficPolicyChange{Aliases: map[string]DeploymentAlias{key: {}}})); err != nil {
		return err
	}
	delete(m.deploymentAliases, key)
	return nil
}

func (m *MemStore) DeploymentAliasByHostLabel(_ context.Context, hostLabel string) (DeploymentAlias, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found DeploymentAlias
	for _, alias := range m.deploymentAliases {
		app, ok := m.apps[alias.AppID]
		if !ok || app.Status == AppDeleted || app.DeletedAt != nil {
			continue
		}
		label, ok := hostidentity.DeploymentAliasLabel(app.ID, alias.Name)
		if !ok || !api.ValidDeploymentAliasName(alias.Name) || label != hostLabel {
			continue
		}
		deployment, ok := m.deployments[alias.DeploymentID]
		if !ok || deployment.DeletedAt != nil || deployment.AppID != app.ID {
			continue
		}
		if found.AppID != "" {
			return DeploymentAlias{}, ErrConflict
		}
		found = alias
		found.Revision = deployment.Revision
	}
	if found.AppID == "" {
		return DeploymentAlias{}, ErrNotFound
	}
	return found, nil
}

// DeploymentAliasReservationStore distinguishes a missing alias from an existing
// alias whose owner or immutable target cannot currently serve requests.
type DeploymentAliasReservationStore interface {
	DeploymentAliasReserved(context.Context, string) (bool, error)
}

func (m *MemStore) DeploymentAliasReserved(ctx context.Context, label string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, alias := range m.deploymentAliases {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		candidate, ok := hostidentity.DeploymentAliasLabel(alias.AppID, alias.Name)
		if ok && candidate == label {
			return true, nil
		}
	}
	return false, ctx.Err()
}
