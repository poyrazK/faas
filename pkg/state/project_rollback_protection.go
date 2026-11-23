package state

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ProjectRollbackProtectionArtifact struct {
	AppID, Service, DeploymentID, Key string
	Bytes                             int64
}

type ProjectRollbackProtectionStore interface {
	ProjectRollbackProtectionArtifacts(context.Context, string, string, string) ([]ProjectRollbackProtectionArtifact, error)
	ProtectProjectRollbackDeploy(context.Context, string, string, string, int, []ProjectRollbackProtectionArtifact) (api.ProjectRollbackProtection, error)
}

type ProjectRollbackProtectionArtifactError struct{ Service string }

func (e *ProjectRollbackProtectionArtifactError) Error() string {
	return fmt.Sprintf("rollback artifact is unavailable for %s", e.Service)
}

func (e *ProjectRollbackProtectionArtifactError) Unwrap() error { return ErrLayerArtifactRetired }

// Inventory is scoped by project ownership and includes the exact layer keys
// the caller must verify before it publishes a protection.
func readProjectRollbackProtectionArtifacts(ctx context.Context, db sqlc.DBTX, account, project, group string) ([]ProjectRollbackProtectionArtifact, error) {
	rows, err := db.Query(ctx, `
		with members as materialized (
		    select d.id, a.id as app_id, case when a.project_id = p.id then a.slug else '' end as slug,
		           coalesce(a.project_id = p.id and a.status <> 'deleted' and (`+projectRollbackRestorableSQL+`), false) as eligible,
		           coalesce(d.rootfs_key, '') as rootfs_key, coalesce(d.rootfs_bytes, 0) as rootfs_bytes
		      from project_deploy_rollback_members m
		      join projects p on p.id = m.project_id and p.account_id = $1
		      join deployments d on d.id = m.deployment_id
		      join apps a on a.id = d.app_id
		     where m.project_id = $2 and m.deploy_group_id = $3
		)
		select m.app_id::text, m.slug, m.id::text, m.eligible, m.rootfs_key, m.rootfs_bytes from members m
		union all
		select m.app_id::text, m.slug, m.id::text, m.eligible, l.storage_key, l.bytes
		  from members m join deployment_sidecar_layers l on l.deployment_id = m.id`, account, project, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var artifacts []ProjectRollbackProtectionArtifact
	for rows.Next() {
		var a ProjectRollbackProtectionArtifact
		var eligible bool
		if err := rows.Scan(&a.AppID, &a.Service, &a.DeploymentID, &eligible, &a.Key, &a.Bytes); err != nil {
			return nil, err
		}
		if !eligible {
			return nil, ErrInvalidArgument
		}
		artifacts = append(artifacts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return validateProjectRollbackProtectionArtifacts(artifacts)
}

func validateProjectRollbackProtectionArtifacts(artifacts []ProjectRollbackProtectionArtifact) ([]ProjectRollbackProtectionArtifact, error) {
	if len(artifacts) == 0 {
		return nil, ErrNotFound
	}
	apps := make(map[string]string)
	for _, a := range artifacts {
		if !validLayerArtifactKey(a.Key) || !validLayerArtifactKey("sigs/"+a.Key+".sig") || a.Bytes <= 0 {
			return nil, &ProjectRollbackProtectionArtifactError{Service: a.Service}
		}
		if dep := apps[a.AppID]; dep != "" && dep != a.DeploymentID {
			return nil, ErrInvalidArgument
		}
		apps[a.AppID] = a.DeploymentID
	}
	slices.SortFunc(artifacts, func(a, b ProjectRollbackProtectionArtifact) int {
		return cmp.Or(cmp.Compare(a.AppID, b.AppID), cmp.Compare(a.DeploymentID, b.DeploymentID),
			cmp.Compare(a.Key, b.Key), cmp.Compare(a.Bytes, b.Bytes))
	})
	return artifacts, nil
}

type projectRollbackProtectionManifest struct {
	Deployments []string
	Keys        []string
}

func projectRollbackProtectionAssets(artifacts []ProjectRollbackProtectionArtifact) (projectRollbackProtectionManifest, map[string]string, int64, error) {
	var manifest projectRollbackProtectionManifest
	deployments := make(map[string]bool)
	bytesByKey := make(map[string]int64)
	services := make(map[string]string)
	for _, a := range artifacts {
		if !deployments[a.DeploymentID] {
			deployments[a.DeploymentID] = true
			manifest.Deployments = append(manifest.Deployments, a.DeploymentID)
		}
		if _, exists := bytesByKey[a.Key]; !exists {
			manifest.Keys = append(manifest.Keys, a.Key)
			services[a.Key] = a.Service
			services["sigs/"+a.Key+".sig"] = a.Service
		}
		bytesByKey[a.Key] = max(bytesByKey[a.Key], a.Bytes)
	}
	var layerBytes int64
	for _, size := range bytesByKey {
		if size > math.MaxInt64-layerBytes {
			return manifest, nil, 0, ErrInvalidArgument
		}
		layerBytes += size
	}
	sort.Strings(manifest.Deployments)
	sort.Strings(manifest.Keys)
	return manifest, services, layerBytes, nil
}

func (m projectRollbackProtectionManifest) fingerprint() string {
	data, _ := json.Marshal(m) // The manifest contains only slices of strings.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *PgStore) ProjectRollbackProtectionArtifacts(ctx context.Context, account, project, group string) ([]ProjectRollbackProtectionArtifact, error) {
	return readProjectRollbackProtectionArtifacts(ctx, s.pool, account, project, group)
}

func (s *PgStore) ProtectProjectRollbackDeploy(ctx context.Context, account, project, group string, seconds int, verified []ProjectRollbackProtectionArtifact) (api.ProjectRollbackProtection, error) {
	out := api.ProjectRollbackProtection{DeployGroupID: group}
	if seconds < 1 || seconds > api.ProjectRollbackProtectionMaxSeconds {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Serialize protection updates and fence project deletion.
	var lockedProject string
	if err := tx.QueryRow(ctx, `select id::text from projects where id = $1 and account_id = $2 for no key update`, project, account).Scan(&lockedProject); err != nil {
		return out, mapErr(err)
	}
	// Sidecar writes lock their deployment. Read a fresh statement snapshot
	// after acquiring the same parent locks so committed sidecars are visible.
	if _, err := tx.Exec(ctx, `
		select d.id from project_deploy_rollback_members m
		join deployments d on d.id = m.deployment_id join apps a on a.id = d.app_id
		where m.project_id = $1 and m.deploy_group_id = $2
		order by a.id, d.id for share of a, d`, project, group); err != nil {
		return out, err
	}
	artifacts, err := readProjectRollbackProtectionArtifacts(ctx, tx, account, project, group)
	if err != nil {
		return out, err
	}
	if !slices.Equal(artifacts, verified) {
		return out, ErrConflict
	}
	manifest, services, layerBytes, err := projectRollbackProtectionAssets(artifacts)
	if err != nil {
		return out, err
	}
	keys := make([]string, 0, len(services))
	for key := range services {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		row, err := lockLayerArtifactTx(ctx, tx, key)
		if err != nil {
			return out, err
		}
		if row.State != LayerArtifactRetained {
			return out, &ProjectRollbackProtectionArtifactError{Service: services[key]}
		}
	}
	if _, err := tx.Exec(ctx, `delete from project_rollback_deploy_protections where project_id = $1 and expires_at <= clock_timestamp()`, project); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `
		insert into project_rollback_deploy_protections as holds
		    (project_id, deploy_group_id, manifest_hash, deployment_ids, storage_keys, expires_at, layer_bytes)
		values ($1, $2, $3, $4, $5, clock_timestamp() + make_interval(secs => $6), $7)
		on conflict (project_id, deploy_group_id, manifest_hash) do update
		set expires_at = greatest(holds.expires_at, excluded.expires_at), layer_bytes = excluded.layer_bytes
		returning expires_at, layer_bytes`, project, group, manifest.fingerprint(),
		manifest.Deployments, manifest.Keys, float64(seconds), layerBytes).Scan(&out.ExpiresAt, &out.LayerBytes)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

type projectRollbackProtectionKey struct{ project, group, manifest string }

type projectRollbackProtectionRecord struct {
	api.ProjectRollbackProtection
	keys []string
}

func (m *MemStore) projectRollbackProtectionArtifactsLocked(account, project, group string) ([]ProjectRollbackProtectionArtifact, error) {
	if p, ok := m.projects[project]; !ok || p.AccountID != account {
		return nil, ErrNotFound
	}
	var artifacts []ProjectRollbackProtectionArtifact
	members := make(map[string]ProjectRollbackProtectionArtifact)
	for _, d := range m.deployments {
		if d.projectDeployProjectID != project || d.ProjectDeployGroupID != group {
			continue
		}
		app := m.apps[d.AppID]
		if app.ProjectID != project || app.Status == AppDeleted || !m.projectRollbackRestorableLocked(d) {
			return nil, ErrInvalidArgument
		}
		a := ProjectRollbackProtectionArtifact{AppID: app.ID, Service: app.Slug, DeploymentID: d.ID, Key: d.RootfsKey, Bytes: d.RootfsBytes}
		members[d.ID] = a
		artifacts = append(artifacts, a)
	}
	for _, layer := range m.deploymentSidecarLayers {
		if a, ok := members[layer.DeploymentID]; ok {
			a.Key, a.Bytes = layer.StorageKey, layer.Bytes
			artifacts = append(artifacts, a)
		}
	}
	return validateProjectRollbackProtectionArtifacts(artifacts)
}

func (m *MemStore) ProjectRollbackProtectionArtifacts(_ context.Context, account, project, group string) ([]ProjectRollbackProtectionArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectRollbackProtectionArtifactsLocked(account, project, group)
}

func (m *MemStore) ProtectProjectRollbackDeploy(ctx context.Context, account, project, group string, seconds int, verified []ProjectRollbackProtectionArtifact) (api.ProjectRollbackProtection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.ProjectRollbackProtection{DeployGroupID: group}
	if seconds < 1 || seconds > api.ProjectRollbackProtectionMaxSeconds {
		return out, ErrInvalidArgument
	}
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return out, err
	}
	artifacts, err := m.projectRollbackProtectionArtifactsLocked(account, project, group)
	if err != nil {
		return out, err
	}
	if !slices.Equal(artifacts, verified) {
		return out, ErrConflict
	}
	manifest, services, layerBytes, err := projectRollbackProtectionAssets(artifacts)
	if err != nil {
		return out, err
	}
	for key, service := range services {
		if row, ok := m.layerArtifactRetention[key]; ok && row.State != LayerArtifactRetained {
			return out, &ProjectRollbackProtectionArtifactError{Service: service}
		}
	}
	now := time.Now()
	for id, hold := range m.projectRollbackProtections {
		if id.project == project && !hold.ExpiresAt.After(now) {
			delete(m.projectRollbackProtections, id)
		}
	}
	id := projectRollbackProtectionKey{project, group, manifest.fingerprint()}
	out.ExpiresAt = now.Add(time.Duration(seconds) * time.Second).UTC()
	if current := m.projectRollbackProtections[id]; current.ExpiresAt.After(out.ExpiresAt) {
		out.ExpiresAt = current.ExpiresAt
	}
	out.LayerBytes = layerBytes
	if m.projectRollbackProtections == nil {
		m.projectRollbackProtections = make(map[projectRollbackProtectionKey]projectRollbackProtectionRecord)
	}
	m.projectRollbackProtections[id] = projectRollbackProtectionRecord{ProjectRollbackProtection: out, keys: manifest.Keys}
	return out, nil
}

func (m *MemStore) projectRollbackProtectsArtifactLocked(key string) bool {
	now := time.Now()
	for id, hold := range m.projectRollbackProtections {
		if _, exists := m.projects[id.project]; !exists || !hold.ExpiresAt.After(now) {
			continue
		}
		if _, ok := slices.BinarySearch(hold.keys, key); ok {
			return true
		}
		if strings.HasPrefix(key, "sigs/") && strings.HasSuffix(key, ".sig") {
			if _, ok := slices.BinarySearch(hold.keys, strings.TrimSuffix(strings.TrimPrefix(key, "sigs/"), ".sig")); ok {
				return true
			}
		}
	}
	return false
}
