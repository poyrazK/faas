package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProjectEnvironmentCloneWorkload is a non-secret view of an immutable source
// capture. The stored artifact and settings are private to the state layer.
type ProjectEnvironmentCloneWorkload struct {
	OperationID        string
	AppID              string
	WorkloadSlug       string
	SourceDeploymentID string
	SourceScope        string
	SourceHash         string
	SourceSettingsHash string
	TargetDeploymentID string
	TargetSettingsHash string
}

type ProjectEnvironmentCloneWorkloadStore interface {
	CaptureProjectEnvironmentCloneWorkloads(context.Context, string, string, string, int64) ([]ProjectEnvironmentCloneWorkload, error)
	ProjectEnvironmentCloneWorkloads(context.Context, string, string, string) ([]ProjectEnvironmentCloneWorkload, error)
	CreateDeploymentForEnvironmentClone(context.Context, string, string, string, int64, string, string) (Deployment, error)
}

type projectEnvironmentCloneDeploymentInput struct {
	AccountID, ProjectID, OperationID, AppID, TargetSettingsHash string
	Revision                                                     int64
}

// Only immutable artifact inputs enter the snapshot. Queue state, rollout
// state, mutable source subscriptions, production memory snapshots and actor
// identity are not replayed into a cloned deployment.
type projectCloneArtifact struct {
	ID                      string          `json:"id"`
	AppID                   string          `json:"app_id"`
	Scope                   string          `json:"scope"`
	Kind                    DeploymentKind  `json:"kind"`
	ImageDigest             string          `json:"image_digest"`
	RootfsPath              string          `json:"rootfs_path"`
	RootfsKey               string          `json:"rootfs_key"`
	RootfsBytes             int64           `json:"rootfs_bytes"`
	Handler                 string          `json:"handler"`
	SourceSHA256            string          `json:"source_sha256"`
	SourceURL               string          `json:"source_url"`
	CommitSHA               string          `json:"commit_sha"`
	OverrideEntrypoint      []string        `json:"override_entrypoint"`
	OverrideCmd             []string        `json:"override_cmd"`
	OverrideEnv             json.RawMessage `json:"override_env"`
	OverrideEnvSecrets      json.RawMessage `json:"override_env_secrets"`
	OverridePort            int             `json:"override_port"`
	OverrideHealthcheck     json.RawMessage `json:"override_healthcheck"`
	OverrideLivenessProbe   json.RawMessage `json:"override_liveness_probe"`
	OverrideReadinessProbe  json.RawMessage `json:"override_readiness_probe"`
	OverrideMainDependsOn   json.RawMessage `json:"override_main_depends_on"`
	Sidecars                json.RawMessage `json:"sidecars"`
	Workflows               json.RawMessage `json:"workflows"`
	FullRootfsAllowAuto     bool            `json:"full_rootfs_allow_auto"`
	FullRootfsOverride      *bool           `json:"full_rootfs_override"`
	InferredProfile         json.RawMessage `json:"inferred_profile"`
	ReleaseCommand          []string        `json:"release_command"`
	ReleaseCommandShell     bool            `json:"release_command_shell"`
	DisableStartupCPUBoost  bool            `json:"disable_startup_cpu_boost"`
	SecretReloadSignal      string          `json:"secret_reload_signal"`
	SecretReloadSignalKnown bool            `json:"secret_reload_signal_known"`
}

func projectCloneArtifactFromDeployment(d Deployment) projectCloneArtifact {
	return projectCloneArtifact{ID: d.ID, AppID: d.AppID, Scope: normalizedDeploymentScope(d.Scope), Kind: d.Kind,
		ImageDigest: d.ImageDigest, RootfsPath: d.RootfsPath, RootfsKey: d.RootfsKey, RootfsBytes: d.RootfsBytes,
		Handler: d.Handler, SourceSHA256: d.SourceSHA256, SourceURL: d.SourceURL, CommitSHA: d.CommitSHA,
		OverrideEntrypoint: d.OverrideEntrypoint, OverrideCmd: d.OverrideCmd, OverrideEnv: d.OverrideEnv,
		OverrideEnvSecrets: d.OverrideEnvSecrets, OverridePort: d.OverridePort, OverrideHealthcheck: d.OverrideHealthcheck,
		OverrideLivenessProbe: d.OverrideLivenessProbe, OverrideReadinessProbe: d.OverrideReadinessProbe,
		OverrideMainDependsOn: d.OverrideMainDependsOn, Sidecars: d.Sidecars, Workflows: d.Workflows,
		FullRootfsAllowAuto: d.FullRootfsAllowAuto, FullRootfsOverride: d.FullRootfsOverride, InferredProfile: d.InferredProfile,
		ReleaseCommand: d.ReleaseCommand, ReleaseCommandShell: d.ReleaseCommandShell,
		DisableStartupCPUBoost: d.DisableStartupCPUBoost, SecretReloadSignal: d.SecretReloadSignal, SecretReloadSignalKnown: d.SecretReloadSignalKnown}
}

func (a projectCloneArtifact) deployment(operationID, target string, settings ProjectEnvironmentWorkloadSettings) Deployment {
	return Deployment{AppID: a.AppID, Scope: target, Kind: a.Kind, ImageDigest: a.ImageDigest,
		RootfsPath: a.RootfsPath, RootfsKey: a.RootfsKey, RootfsBytes: a.RootfsBytes,
		Handler: a.Handler, SourceSHA256: a.SourceSHA256, SourceURL: a.SourceURL, CommitSHA: a.CommitSHA,
		OverrideEntrypoint: a.OverrideEntrypoint, OverrideCmd: a.OverrideCmd, OverrideEnv: a.OverrideEnv,
		OverrideEnvSecrets: a.OverrideEnvSecrets, OverridePort: a.OverridePort, OverrideHealthcheck: a.OverrideHealthcheck,
		OverrideLivenessProbe: a.OverrideLivenessProbe, OverrideReadinessProbe: a.OverrideReadinessProbe,
		OverrideMainDependsOn: a.OverrideMainDependsOn, Sidecars: a.Sidecars, Workflows: a.Workflows,
		FullRootfsAllowAuto: a.FullRootfsAllowAuto, FullRootfsOverride: a.FullRootfsOverride, InferredProfile: a.InferredProfile,
		ReleaseCommand: a.ReleaseCommand, ReleaseCommandShell: a.ReleaseCommandShell,
		DisableStartupCPUBoost: a.DisableStartupCPUBoost, SecretReloadSignal: a.SecretReloadSignal, SecretReloadSignalKnown: a.SecretReloadSignalKnown,
		MinInstances: settings.MinInstances, Status: DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true,
		Reason: "environment-clone:" + operationID, DeployedVia: "api", RolloutState: "pending", CanaryPreset: "none"}
}

type projectCloneWorkloadSnapshot struct {
	WorkloadSlug   string                             `json:"workload_slug"`
	Artifact       projectCloneArtifact               `json:"artifact"`
	Settings       ProjectEnvironmentWorkloadSettings `json:"settings"`
	Layers         []DeploymentSidecarLayer           `json:"layers"`
	SidecarSignals map[string]string                  `json:"sidecar_signals"`
}

type projectCloneWorkloadRecord struct {
	ProjectEnvironmentCloneWorkload
	snapshot projectCloneWorkloadSnapshot
}

func encodeCloneWorkloadSnapshot(snapshot projectCloneWorkloadSnapshot) ([]byte, string, error) {
	snapshot.Artifact = normalizeProjectCloneArtifact(snapshot.Artifact)
	if snapshot.Artifact.ID == "" || snapshot.Artifact.AppID == "" || snapshot.WorkloadSlug == "" ||
		(snapshot.Artifact.RootfsKey == "" && snapshot.Artifact.RootfsPath == "") || snapshot.Artifact.RootfsBytes <= 0 {
		return nil, "", ErrConflict
	}
	if _, err := WorkloadSettingsHash(snapshot.Settings); err != nil {
		return nil, "", err
	}
	if err := validateDeploymentReleaseCommand(snapshot.Artifact.ReleaseCommand, snapshot.Artifact.ReleaseCommandShell); err != nil {
		return nil, "", err
	}
	sort.Slice(snapshot.Layers, func(i, j int) bool { return snapshot.Layers[i].SidecarName < snapshot.Layers[j].SidecarName })
	for i := range snapshot.Layers {
		layer := &snapshot.Layers[i]
		if layer.DeploymentID != snapshot.Artifact.ID || layer.SidecarName == "" || layer.StorageKey == "" || layer.Bytes <= 0 ||
			(i > 0 && snapshot.Layers[i-1].SidecarName == layer.SidecarName) {
			return nil, "", ErrConflict
		}
		// These timestamps describe materialization, not artifact identity.
		layer.CreatedAt, layer.UpdatedAt = time.Time{}, time.Time{}
	}
	var sidecars []api.Sidecar
	if len(snapshot.Artifact.Sidecars) > 0 && string(snapshot.Artifact.Sidecars) != "null" {
		if err := json.Unmarshal(snapshot.Artifact.Sidecars, &sidecars); err != nil {
			return nil, "", ErrConflict
		}
	}
	if len(sidecars) != len(snapshot.Layers) {
		return nil, "", ErrConflict
	}
	names := map[string]bool{}
	for _, sidecar := range sidecars {
		if sidecar.Name == "" || names[sidecar.Name] {
			return nil, "", ErrConflict
		}
		names[sidecar.Name] = true
	}
	for _, layer := range snapshot.Layers {
		if !names[layer.SidecarName] {
			return nil, "", ErrConflict
		}
	}
	for name, signal := range snapshot.SidecarSignals {
		if !names[name] || !validSecretReloadSignal(signal) {
			return nil, "", ErrConflict
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", err
	}
	// jsonb reorders object keys. Canonicalize nested JSON before hashing, and
	// retain numbers exactly rather than rounding through float64.
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&canonical); err != nil {
		return nil, "", err
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(raw)
	return raw, hex.EncodeToString(hash[:]), nil
}

func normalizeProjectCloneArtifact(a projectCloneArtifact) projectCloneArtifact {
	// Nullable SQL arrays read back as empty slices, while JSON source rows
	// retain null. Both describe an absent argv/release command.
	if len(a.OverrideEntrypoint) == 0 {
		a.OverrideEntrypoint = nil
	}
	if len(a.OverrideCmd) == 0 {
		a.OverrideCmd = nil
	}
	if len(a.ReleaseCommand) == 0 {
		a.ReleaseCommand = nil
	}
	for _, value := range []*json.RawMessage{&a.OverrideEnv, &a.OverrideEnvSecrets, &a.OverrideHealthcheck,
		&a.OverrideLivenessProbe, &a.OverrideReadinessProbe, &a.InferredProfile} {
		if bytes.Equal(bytes.TrimSpace(*value), []byte("null")) {
			*value = nil
		}
	}
	for _, value := range []*json.RawMessage{&a.Sidecars, &a.Workflows, &a.OverrideMainDependsOn} {
		if len(*value) == 0 || bytes.Equal(bytes.TrimSpace(*value), []byte("null")) {
			*value = json.RawMessage(`[]`)
		}
	}
	return a
}

func copyCloneWorkloadRecord(record projectCloneWorkloadRecord) (projectCloneWorkloadRecord, error) {
	raw, hash, err := encodeCloneWorkloadSnapshot(record.snapshot)
	if err != nil || hash != record.SourceHash {
		return projectCloneWorkloadRecord{}, ErrConflict
	}
	return decodeCloneWorkloadRecord(record.OperationID, record.AppID, record.SourceDeploymentID, record.SourceHash,
		record.TargetDeploymentID, record.TargetSettingsHash, raw)
}

func decodeCloneWorkloadRecord(operationID, appID, sourceID, sourceHash, targetID, targetHash string, raw []byte) (projectCloneWorkloadRecord, error) {
	var snapshot projectCloneWorkloadSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return projectCloneWorkloadRecord{}, ErrConflict
	}
	_, hash, err := encodeCloneWorkloadSnapshot(snapshot)
	if err != nil || hash != sourceHash || snapshot.Artifact.ID != sourceID || snapshot.Artifact.AppID != appID {
		return projectCloneWorkloadRecord{}, ErrConflict
	}
	settingsHash, _ := WorkloadSettingsHash(snapshot.Settings)
	return projectCloneWorkloadRecord{ProjectEnvironmentCloneWorkload: ProjectEnvironmentCloneWorkload{
		OperationID: operationID, AppID: appID, WorkloadSlug: snapshot.WorkloadSlug, SourceDeploymentID: sourceID,
		SourceScope: snapshot.Artifact.Scope, SourceHash: hash, SourceSettingsHash: settingsHash,
		TargetDeploymentID: targetID, TargetSettingsHash: targetHash}, snapshot: snapshot}, nil
}
