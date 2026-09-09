package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// captureDeploymentOpenAPISnapshotLocked mirrors the Postgres capture path:
// enabled edge rules are ordered like ListEdgeRulesForApp and projected by
// the registered callback. The caller must hold m.mu.
func (m *MemStore) captureDeploymentOpenAPISnapshotLocked(ctx context.Context, d Deployment) (OpenAPISnapshot, error) {
	rules := make([]EdgeRule, 0)
	for _, rule := range m.edgeRules {
		if rule.AppID == d.AppID && rule.Enabled {
			rules = append(rules, rule)
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority < rules[j].Priority
		}
		return rules[i].CreatedAt.After(rules[j].CreatedAt)
	})
	pending := make([]api.CreateEdgeRuleRequest, 0, len(rules))
	for _, rule := range rules {
		request, err := edgeRuleToCreateEdgeRuleRequest(rule)
		if err != nil {
			return OpenAPISnapshot{}, fmt.Errorf("memstore: encode edge rule %s for snapshot: %w", rule.ID, err)
		}
		pending = append(pending, request)
	}
	var importedDoc []byte
	if imported, ok := m.openAPIImports[d.AppID]; ok {
		importedDoc = append([]byte(nil), imported.Doc...)
	}
	snap, err := getOpenAPICapture()(ctx, nil, d.ID, d.AppID, normalizedDeploymentScope(d.Scope), pending, importedDoc)
	if err != nil {
		return OpenAPISnapshot{}, fmt.Errorf("memstore: capture snapshot for %s: %w", d.ID, err)
	}
	return snap, nil
}

// storeOpenAPISnapshotLocked stores a callback result. A zero snapshot is the
// explicit no-op signal used by processes that do not register a projector.
// The caller must hold m.mu.
func (m *MemStore) storeOpenAPISnapshotLocked(snap OpenAPISnapshot) error {
	if snap.DeploymentID == "" && len(snap.Snapshot) == 0 && snap.SHA256 == "" {
		return nil
	}
	if err := validateOpenAPISnapshot(snap); err != nil {
		return fmt.Errorf("memstore: openapi snapshot: %w", err)
	}
	if snap.CapturedAt.IsZero() {
		snap.CapturedAt = time.Now().UTC()
	}
	m.openAPISnapshots[snap.DeploymentID] = snap
	return nil
}

// UpdateDeploymentOpenAPISnapshot mirrors the Postgres UPSERT for tests and
// local development.
func (m *MemStore) UpdateDeploymentOpenAPISnapshot(_ context.Context, snap OpenAPISnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if snap.DeploymentID == "" {
		return errors.New("memstore: UpdateDeploymentOpenAPISnapshot: empty deployment_id")
	}
	if snap.AppID == "" {
		return errors.New("memstore: UpdateDeploymentOpenAPISnapshot: empty app_id")
	}
	if snap.Scope == "" {
		return errors.New("memstore: UpdateDeploymentOpenAPISnapshot: empty scope")
	}
	if len(snap.Snapshot) == 0 {
		return errors.New("memstore: UpdateDeploymentOpenAPISnapshot: empty snapshot bytes")
	}
	if snap.SHA256 == "" {
		return errors.New("memstore: UpdateDeploymentOpenAPISnapshot: empty sha256")
	}
	if snap.SchemaVersion < 1 {
		return errors.New("memstore: UpdateDeploymentOpenAPISnapshot: schema_version must be >= 1")
	}
	if err := m.storeOpenAPISnapshotLocked(snap); err != nil {
		return err
	}
	return nil
}

// LatestOpenAPISnapshotForScope returns the newest snapshot for an app and
// deployment scope.
func (m *MemStore) LatestOpenAPISnapshotForScope(_ context.Context, appID, scope string) (OpenAPISnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scope = normalizedDeploymentScope(scope)
	var best OpenAPISnapshot
	found := false
	for _, snap := range m.openAPISnapshots {
		if snap.AppID != appID || normalizedDeploymentScope(snap.Scope) != scope {
			continue
		}
		if !found || snap.CapturedAt.After(best.CapturedAt) {
			best = snap
			found = true
		}
	}
	if !found {
		return OpenAPISnapshot{}, ErrNotFound
	}
	return best, nil
}

// OpenAPISnapshotByDeployment returns the snapshot for a deployment.
func (m *MemStore) OpenAPISnapshotByDeployment(_ context.Context, deploymentID string) (OpenAPISnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, ok := m.openAPISnapshots[deploymentID]
	if !ok {
		return OpenAPISnapshot{}, ErrNotFound
	}
	return snap, nil
}
