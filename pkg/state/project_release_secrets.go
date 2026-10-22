package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Private sealed material: never copied into receipts or configuration metadata.
type releaseSecretVersion struct {
	ID    string `json:"id"`
	AppID string `json:"app_id"`
	runtimeAppSealedSecretRow
}

type ProjectReleaseSealedSecret struct {
	Workload            string
	AppID               string
	Key, Kid, ValueHash string
	Ciphertext          []byte
}

type releaseSecretBundle struct {
	AccountID, ProjectID, RequestID, RequestHash string
	Values                                       []releaseSecretVersion
	Response                                     api.ProjectReleaseSecretStageResponse
	CreatedAt                                    time.Time
}

type ProjectReleaseSecretsStore interface {
	StageProjectReleaseSecrets(context.Context, string, string, string, string, []ProjectReleaseSealedSecret, map[string]map[string]string) (api.ProjectReleaseSecretStageResponse, error)
	ReleaseSecretsPinned(context.Context, string, string, string) (bool, error)
}

func (v releaseSecretVersion) secret(account, scope string) AppSecret {
	r := v.runtimeAppSealedSecretRow
	return AppSecret{AccountID: account, AppID: v.AppID, Scope: scope, Key: r.Key, Ciphertext: append([]byte(nil), r.Ciphertext...), Kid: r.Kid, ValueHash: r.ValueHash, SecretClass: r.SecretClass, SecretVersion: r.SecretVersion, DeliveryVersion: r.DeliveryVersion, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func releaseSecretFromRow(app string, row runtimeAppSealedSecretRow) releaseSecretVersion {
	row.CreatedAt, row.UpdatedAt = row.CreatedAt.UTC().Truncate(time.Microsecond), row.UpdatedAt.UTC().Truncate(time.Microsecond)
	encoded, _ := json.Marshal(struct {
		App string
		Row runtimeAppSealedSecretRow
	}{app, row})
	return releaseSecretVersion{uuid.NewSHA1(uuid.NameSpaceOID, encoded).String(), app, row}
}
func releaseSecretRow(secret AppSecret) runtimeAppSealedSecretRow {
	return runtimeAppSealedSecretRow{Key: secret.Key, Ciphertext: append([]byte(nil), secret.Ciphertext...), SecretClass: defaultSecretClass(secret.SecretClass), Kid: secret.Kid, ValueHash: secret.ValueHash, SecretVersion: secret.SecretVersion, DeliveryVersion: secret.DeliveryVersion, CreatedAt: secret.CreatedAt, UpdatedAt: secret.UpdatedAt, ManagedPostgresBindingID: secret.ManagedPostgresBindingID, ManagedPostgresAccess: secret.ManagedPostgresAccess, ManagedCredentialRef: secret.ManagedCredentialRef, ManagedCredentialGeneration: secret.ManagedCredentialGeneration, ManagedObjectStorageCredentialID: secret.ManagedObjectStorageCredentialID}
}
func releaseSecretRowsHash(rows []runtimeAppSealedSecretRow) string {
	copy := append([]runtimeAppSealedSecretRow{}, rows...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Key < copy[j].Key })
	raw, _ := json.Marshal(copy)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func cloneReleaseSecrets(rows []releaseSecretVersion) []releaseSecretVersion {
	raw, _ := json.Marshal(rows)
	var out []releaseSecretVersion
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = []releaseSecretVersion{}
	}
	return out
}
func (capture *projectReleaseConfigCapture) prepareSecrets(input api.ProjectReleaseConfigRequest, limits api.Limits) error {
	required := len(input.Secrets) > 0
	for _, w := range capture.Workloads {
		required = required || w.SecretSnapshot != nil
	}
	if !required {
		return nil
	}
	used := map[string]bool{}
	versions := map[string]map[string]string{}
	previousVersions := map[string]map[string]string{}
	for i := range capture.Workloads {
		w := &capture.Workloads[i]
		if w.SecretIneligible {
			return fmt.Errorf("release secrets require application-owned persistent secrets without managed bindings, sidecars, explicit grants or reload hooks")
		}
		w.SecretTarget = []releaseSecretVersion{}
		for _, row := range w.LiveSecrets {
			if row.ManagedPostgresBindingID != "" || row.ManagedObjectStorageCredentialID != "" || defaultSecretClass(row.SecretClass) != api.SecretClassPersistent {
				return fmt.Errorf("release secrets cannot capture managed or ephemeral credentials")
			}
			w.SecretTarget = append(w.SecretTarget, releaseSecretFromRow(w.AppID, row))
		}
		if w.SecretSnapshot != nil {
			w.SecretTarget = cloneReleaseSecrets(*w.SecretSnapshot)
		}
		previousVersions[w.Name] = map[string]string{}
		for _, row := range w.SecretTarget {
			previousVersions[w.Name][row.Key] = row.ID
		}
		if requested, ok := input.Secrets[w.Name]; ok {
			used[w.Name] = true
			w.SecretTarget = []releaseSecretVersion{}
			for key, id := range requested {
				row, found := capture.SecretVersions[id]
				existing := false
				for _, live := range w.LiveSecrets {
					existing = existing || live.Key == key
				}
				if !existing {
					return fmt.Errorf("release secret rotation requires an existing production secret key")
				}
				if !found || row.AppID != w.AppID || row.Key != key {
					return fmt.Errorf("release secret version is missing, expired or belongs to another workload")
				}
				w.SecretTarget = append(w.SecretTarget, row)
			}
		}
		if len(w.SecretTarget) > limits.SecretCountMax {
			return fmt.Errorf("release secret snapshot exceeds the plan's secret limit")
		}
		sort.Slice(w.SecretTarget, func(i, j int) bool { return w.SecretTarget[i].Key < w.SecretTarget[j].Key })
		versions[w.Name] = map[string]string{}
		for _, row := range w.SecretTarget {
			versions[w.Name][row.Key] = row.ID
			for _, variable := range w.Target {
				if variable.Key == row.Key {
					return fmt.Errorf("release variables cannot shadow secret versions")
				}
			}
		}
	}
	if len(used) != len(input.Secrets) {
		return fmt.Errorf("release secrets reference an unknown workload")
	}
	capture.Descriptor.Secrets = &api.ProjectReleaseSecretConfiguration{Versions: versions, PreviousVersions: previousVersions}
	return nil
}
func (m *MemStore) releaseSecretsPinnedLocked(appID, scope string) bool {
	app := m.apps[appID]
	release := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(app.ProjectID, normalizedDeploymentScope(scope))]]
	for _, member := range release.Members {
		if member.AppID == appID {
			_, ok := m.releaseSecretPins[member.DeploymentID]
			return ok
		}
	}
	return false
}
func (m *MemStore) ReleaseSecretsPinned(_ context.Context, account, appID, scope string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apps[appID].AccountID != account {
		return false, ErrNotFound
	}
	return m.releaseSecretsPinnedLocked(appID, scope), nil
}
func releaseSecretRequestIDs(raw json.RawMessage) []string {
	var req api.ProjectReleaseConfigRequest
	_ = json.Unmarshal(raw, &req)
	ids := []string{}
	for _, keys := range req.Secrets {
		for _, id := range keys {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
func (m *MemStore) loadReleaseSecretVersionsLocked(capture *projectReleaseConfigCapture, account string, request json.RawMessage) {
	capture.SecretVersions = map[string]releaseSecretVersion{}
	ids := releaseSecretRequestIDs(request)
	for _, bundle := range m.releaseSecretBundles {
		if bundle.AccountID != account || bundle.ProjectID != capture.ProjectID || time.Since(bundle.CreatedAt) >= api.ProjectReleaseSecretStageTTL {
			continue
		}
		for _, row := range bundle.Values {
			for _, id := range ids {
				if row.ID == id {
					capture.SecretVersions[id] = row
				}
			}
		}
	}
}
func (m *MemStore) StageProjectReleaseSecrets(_ context.Context, account, project, request, hash string, rows []ProjectReleaseSealedSecret, workloads map[string]map[string]string) (api.ProjectReleaseSecretStageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projects[project].AccountID != account {
		return api.ProjectReleaseSecretStageResponse{}, ErrNotFound
	}
	if m.releaseSecretBundles == nil {
		m.releaseSecretBundles = map[string]releaseSecretBundle{}
	}
	key := account + "\x00" + request
	if prior, ok := m.releaseSecretBundles[key]; ok {
		if prior.ProjectID != project || prior.RequestHash != hash {
			return api.ProjectReleaseSecretStageResponse{}, ErrConflict
		}
		return cloneReleaseSecretStageResponse(prior.Response), nil
	}
	count := 0
	for id, prior := range m.releaseSecretBundles {
		if prior.ProjectID == project && time.Since(prior.CreatedAt) >= api.ProjectReleaseSecretStageTTL {
			delete(m.releaseSecretBundles, id)
			continue
		}
		if prior.ProjectID == project && time.Since(prior.CreatedAt) < api.ProjectReleaseSecretStageTTL {
			count++
		}
	}
	if count >= api.ProjectReleaseSecretStagesPerProject {
		return api.ProjectReleaseSecretStageResponse{}, ErrConflict
	}
	bundle, err := newReleaseSecretBundle(account, project, request, hash, rows, workloads)
	if err != nil {
		return api.ProjectReleaseSecretStageResponse{}, err
	}
	for _, row := range bundle.Values {
		if !m.secretEncryptionKeyAllowedLocked(row.Kid) {
			return api.ProjectReleaseSecretStageResponse{}, ErrConflict
		}
		if app := m.apps[row.AppID]; app.AccountID != account || app.ProjectID != project || app.Status == AppDeleted {
			return api.ProjectReleaseSecretStageResponse{}, ErrConflict
		}
	}
	m.releaseSecretBundles[key] = bundle
	return cloneReleaseSecretStageResponse(bundle.Response), nil
}
func newReleaseSecretBundle(account, project, request, hash string, rows []ProjectReleaseSealedSecret, workloads map[string]map[string]string) (releaseSecretBundle, error) {
	b := releaseSecretBundle{AccountID: account, ProjectID: project, RequestID: request, RequestHash: hash, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Values: []releaseSecretVersion{}, Response: api.ProjectReleaseSecretStageResponse{RequestID: request, Versions: map[string]map[string]string{}}}
	if !api.ValidDeploymentAttemptID(request) || !validSecretRevision(hash) {
		return b, ErrInvalidArgument
	}
	for name := range workloads {
		b.Response.Versions[name] = map[string]string{}
	}
	for _, row := range rows {
		if _, exists := b.Response.Versions[row.Workload]; !exists || api.ValidateSecretKey(row.Key) != nil || len(row.Ciphertext) == 0 || row.Kid == "" || row.AppID == "" {
			return b, ErrInvalidArgument
		}
		if _, duplicate := b.Response.Versions[row.Workload][row.Key]; duplicate {
			return b, ErrInvalidArgument
		}
		version := releaseSecretVersion{ID: uuid.NewString(), AppID: row.AppID, runtimeAppSealedSecretRow: runtimeAppSealedSecretRow{Key: row.Key, Ciphertext: append([]byte(nil), row.Ciphertext...), Kid: row.Kid, ValueHash: row.ValueHash, SecretClass: api.SecretClassPersistent, SecretVersion: 1, DeliveryVersion: 1, CreatedAt: b.CreatedAt, UpdatedAt: b.CreatedAt}}
		b.Values = append(b.Values, version)
		b.Response.Versions[row.Workload][row.Key] = version.ID
	}
	return b, nil
}
func cloneReleaseSecretStageResponse(r api.ProjectReleaseSecretStageResponse) api.ProjectReleaseSecretStageResponse {
	out := r
	out.Versions = map[string]map[string]string{}
	for name, values := range r.Versions {
		out.Versions[name] = maps.Clone(values)
	}
	return out
}
