package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeUpgradeTargetStore is the preparation seam for a future apid-owned
// updater. A target must be pinned before a build is queued. This is immutable
// build input, not target qualification or permission to activate a release.
// No customer mutation route calls this seam yet.
type RuntimeUpgradeTargetStore interface {
	PinDeploymentRuntimeUpgradeTarget(context.Context, string, string, string) error
	DeploymentRuntimeUpgradeTarget(context.Context, string) (RuntimeRelease, error)
}

type runtimeUpgradeTarget struct {
	ReleaseID    string
	SourceSHA256 string
	SourceRoot   string
	SourceBytes  int64
	Kind         DeploymentKind
	Handler      string
}

func runtimeUpgradeTargetInput(d Deployment, releaseID string) runtimeUpgradeTarget {
	return runtimeUpgradeTarget{releaseID, d.SourceSHA256, d.SourceRoot, d.SourceBytes, d.Kind, d.Handler}
}

func (p runtimeUpgradeTarget) matches(d Deployment) bool {
	return p == runtimeUpgradeTargetInput(d, p.ReleaseID)
}

func validateRuntimeUpgradeTarget(app App, dep Deployment, target RuntimeRelease) error {
	if err := target.Validate(); err != nil {
		return err
	}
	if app.ID != dep.AppID || app.Type != AppTypeFunction || app.Runtime != target.Runtime ||
		app.Manifest.BuildDockerfile != "" || dep.EnvironmentWorkloadHeld() ||
		(dep.Kind != DeploymentKindTarball && dep.Kind != DeploymentKindGitHub && dep.Kind != DeploymentKindPreview) ||
		!runtimeSHA.MatchString(dep.SourceSHA256) || dep.SourceBytes <= 0 ||
		len(dep.SourceRoot) > api.RuntimeUpgradeSourceFieldMaxBytes || len(dep.Handler) > api.RuntimeUpgradeSourceFieldMaxBytes {
		return fmt.Errorf("%w: runtime upgrade requires a managed function and recorded source", ErrConflict)
	}
	return nil
}

var _ RuntimeUpgradeTargetStore = (*MemStore)(nil)

func (m *MemStore) PinDeploymentRuntimeUpgradeTarget(_ context.Context, depID, releaseID, sourceSHA256 string) error {
	if !runtimeSHA.MatchString(releaseID) || !runtimeSHA.MatchString(sourceSHA256) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[depID]
	if !ok {
		return ErrNotFound
	}
	r, ok := m.runtimeReleases[releaseID]
	if !ok {
		return ErrNotFound
	}
	app, ok := m.apps[d.AppID]
	if !ok || app.Status != AppActive || d.Status != DeployPending || d.SourceSHA256 != sourceSHA256 ||
		d.RootfsKey != "" || d.RootfsPath != "" || d.ImageDigest != "" {
		return ErrConflict
	}
	if err := validateRuntimeUpgradeTarget(app, d, r); err != nil {
		return err
	}
	for _, build := range m.builds {
		if build.DeploymentID == depID {
			return ErrConflict
		}
	}
	pin := runtimeUpgradeTargetInput(d, releaseID)
	if old, ok := m.runtimeUpgradeTargets[depID]; ok && old != pin {
		return ErrConflict
	}
	if m.runtimeUpgradeTargets == nil {
		m.runtimeUpgradeTargets = make(map[string]runtimeUpgradeTarget)
	}
	m.runtimeUpgradeTargets[depID] = pin
	return nil
}

func (m *MemStore) DeploymentRuntimeUpgradeTarget(_ context.Context, depID string) (RuntimeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pin, ok := m.runtimeUpgradeTargets[depID]
	if !ok {
		return RuntimeRelease{}, ErrNotFound
	}
	d, ok := m.deployments[depID]
	if !ok || !pin.matches(d) {
		return RuntimeRelease{}, ErrConflict
	}
	r, ok := m.runtimeReleases[pin.ReleaseID]
	if !ok {
		return RuntimeRelease{}, ErrConflict
	}
	return r, nil
}
