package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type DeploymentStagingParams struct {
	AccountID string
	Request   api.DeploymentStagingRequest
	// Prepare validates the proposed policies against the locked current app.
	// It must be pure and may not call the store.
	Prepare func(App, map[string]json.RawMessage) (AppManifest, error)
}

type DeploymentStagingResult struct {
	Receipt api.DeploymentStagingReceipt
	App     App
	Changed bool
}

type DeploymentStagingStore interface {
	DeploymentStagingSnapshot(context.Context, string, string) (api.DeploymentStagingSnapshot, error)
	ApplyDeploymentStaging(context.Context, string, DeploymentStagingParams) (DeploymentStagingResult, error)
}

var ErrDeploymentStagingConflict = errors.New("deployment staging precondition changed")

type deploymentPolicyState struct {
	Revision    int64
	Owner       string
	Fingerprint string
}

func deploymentPolicySettings(m AppManifest) map[string]json.RawMessage {
	profiling, _ := json.Marshal(m.Profiling)
	tracing, _ := json.Marshal(m.Tracing)
	return map[string]json.RawMessage{"profiling": profiling, "tracing": tracing}
}

func policiesMatch(current AppManifest, expected map[string]json.RawMessage) bool {
	values := deploymentPolicySettings(current)
	for field, value := range expected {
		if !api.PolicySettingEqual(field, values[field], value) {
			return false
		}
	}
	return true
}

func prepareDeploymentStaging(app App, latest string, meta deploymentPolicyState, p DeploymentStagingParams) (DeploymentStagingResult, deploymentPolicyState, error) {
	r := p.Request
	result := DeploymentStagingResult{App: app, Receipt: api.DeploymentStagingReceipt{SessionID: r.SessionID, AppID: app.ID, Revision: meta.Revision}}
	conflict := func() (DeploymentStagingResult, deploymentPolicyState, error) {
		return result, meta, ErrDeploymentStagingConflict
	}
	if app.ID != r.ExpectedAppID || app.AccountID != p.AccountID || app.Status == AppDeleted || latest != r.ExpectedDeploymentID {
		return conflict()
	}
	if err := r.Validate(); err != nil {
		return result, meta, ErrInvalidArgument
	}
	fingerprint, err := r.Fingerprint()
	if err != nil {
		return result, meta, err
	}
	var target map[string]json.RawMessage
	if r.Operation == "stage" {
		result.Receipt.Status = "staged"
		if meta.Owner == r.SessionID && meta.Fingerprint == fingerprint && meta.Revision == r.ExpectedRevision+1 && policiesMatch(app.Manifest, r.Desired) {
			return result, meta, nil
		}
		if meta.Owner == r.SessionID || meta.Revision != r.ExpectedRevision || !policiesMatch(app.Manifest, r.Before) {
			return conflict()
		}
		target = r.Desired
		meta.Owner, meta.Fingerprint = r.SessionID, fingerprint
	} else {
		result.Receipt.Status = "cleaned"
		owned := meta.Owner == r.SessionID && meta.Fingerprint == fingerprint && meta.Revision == r.ExpectedRevision+1 && policiesMatch(app.Manifest, r.Desired)
		if owned {
			target = r.Before
			meta.Owner, meta.Fingerprint = "", ""
		} else if meta.Revision != r.ExpectedRevision && policiesMatch(app.Manifest, r.Before) {
			// The original stage is fenced; preserve any newer owner or edit.
			return result, meta, nil
		} else if meta.Revision == r.ExpectedRevision && policiesMatch(app.Manifest, r.Before) {
			// Seal a never-applied stage, including a request still in flight.
			meta.Revision++
			result.Receipt.Revision = meta.Revision
			return result, meta, nil
		} else {
			return conflict()
		}
	}
	if p.Prepare == nil {
		return result, meta, ErrInvalidArgument
	}
	manifest, err := p.Prepare(app, target)
	if err != nil {
		return result, meta, err
	}
	result.App.Manifest = manifest
	result.Changed = !policiesMatch(app.Manifest, target)
	meta.Revision++
	result.Receipt.Revision = meta.Revision
	return result, meta, nil
}

func (m *MemStore) latestPolicyDeploymentLocked(appID string) string {
	var latest Deployment
	for _, d := range m.deployments {
		if d.AppID == appID && (latest.ID == "" || d.CreatedAt.After(latest.CreatedAt) || (d.CreatedAt.Equal(latest.CreatedAt) && d.ID > latest.ID)) {
			latest = d
		}
	}
	return latest.ID
}

func (m *MemStore) DeploymentStagingSnapshot(_ context.Context, accountID, appID string) (api.DeploymentStagingSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.DeploymentStagingSnapshot{}, ErrNotFound
	}
	return api.DeploymentStagingSnapshot{AppID: appID, Revision: m.deploymentPolicyStates[appID].Revision, LatestDeploymentID: m.latestPolicyDeploymentLocked(appID), Settings: deploymentPolicySettings(app.Manifest)}, nil
}

func (m *MemStore) ApplyDeploymentStaging(_ context.Context, appID string, p DeploymentStagingParams) (DeploymentStagingResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return DeploymentStagingResult{}, ErrNotFound
	}
	result, meta, err := prepareDeploymentStaging(app, m.latestPolicyDeploymentLocked(appID), m.deploymentPolicyStates[appID], p)
	if err != nil {
		return result, err
	}
	if result.Changed {
		manifest := result.App.Manifest
		result.App, err = m.syncProductionWorkloadSpecLocked(result.App, UpdateAppParams{Manifest: &manifest})
		if err != nil {
			return DeploymentStagingResult{}, err
		}
		m.apps[appID] = cloneDeploymentPolicyPointers(result.App)
	}
	if m.deploymentPolicyStates == nil {
		m.deploymentPolicyStates = make(map[string]deploymentPolicyState)
	}
	m.deploymentPolicyStates[appID] = meta
	return result, nil
}

func (m *MemStore) invalidateDeploymentPoliciesLocked(before, after App, touch bool) {
	if !touch && policiesMatch(before.Manifest, deploymentPolicySettings(after.Manifest)) {
		return
	}
	if m.deploymentPolicyStates == nil {
		m.deploymentPolicyStates = make(map[string]deploymentPolicyState)
	}
	meta := m.deploymentPolicyStates[after.ID]
	meta.Revision++
	meta.Owner, meta.Fingerprint = "", ""
	m.deploymentPolicyStates[after.ID] = meta
}

func (s *PgStore) DeploymentStagingSnapshot(ctx context.Context, accountID, appID string) (api.DeploymentStagingSnapshot, error) {
	row, err := sqlc.New().ReadDeploymentPolicySnapshot(ctx, s.pool, sqlc.ReadDeploymentPolicySnapshotParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return api.DeploymentStagingSnapshot{}, mapErr(err)
	}
	var manifest AppManifest
	if err := json.Unmarshal(row.Manifest, &manifest); err != nil {
		return api.DeploymentStagingSnapshot{}, err
	}
	return api.DeploymentStagingSnapshot{AppID: row.AppID, Revision: row.DeploymentPolicyRevision, LatestDeploymentID: row.LatestDeploymentID, Settings: deploymentPolicySettings(manifest)}, nil
}

func (s *PgStore) ApplyDeploymentStaging(ctx context.Context, appID string, p DeploymentStagingParams) (DeploymentStagingResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DeploymentStagingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().ReadDeploymentPolicyState(ctx, tx, appID)
	if err != nil {
		return DeploymentStagingResult{}, mapErr(err)
	}
	app, err := appByIDDB(ctx, tx, appID)
	if err != nil {
		return DeploymentStagingResult{}, err
	}
	latest, err := sqlc.New().ReadDeploySettingsLatest(ctx, tx, appID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DeploymentStagingResult{}, err
	}
	meta := deploymentPolicyState{Revision: row.DeploymentPolicyRevision, Owner: row.DeploymentPolicyOwner, Fingerprint: row.DeploymentPolicyFingerprint}
	result, next, err := prepareDeploymentStaging(app, latest.ID, meta, p)
	if err != nil {
		return result, err
	}
	if next != meta {
		fields := make(map[string]json.RawMessage, len(p.Request.Desired))
		all := deploymentPolicySettings(result.App.Manifest)
		for field := range p.Request.Desired {
			fields[field] = all[field]
		}
		data, err := json.Marshal(fields)
		if err != nil {
			return result, err
		}
		err = sqlc.New().WriteDeploymentPolicyState(ctx, tx, sqlc.WriteDeploymentPolicyStateParams{AppID: appID, Policies: data, SetPolicies: result.Changed, Revision: next.Revision, Owner: next.Owner, Fingerprint: next.Fingerprint})
		if err != nil {
			return result, mapErr(err)
		}
		if result.Changed {
			manifest := result.App.Manifest
			result.App, err = syncProductionWorkloadSpecTx(ctx, tx, result.App, UpdateAppParams{Manifest: &manifest})
			if err != nil {
				return result, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("state: commit deployment policy staging: %w", err)
	}
	return result, nil
}

func cloneDeploymentPolicyPointers(app App) App {
	if app.Manifest.Profiling != nil {
		copy := *app.Manifest.Profiling
		app.Manifest.Profiling = &copy
	}
	if app.Manifest.Tracing != nil {
		copy := *app.Manifest.Tracing
		app.Manifest.Tracing = &copy
	}
	return app
}
