package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// This request intentionally has no caller-supplied source revision or release.
// The state transaction selects and captures the actual source configuration.
type ProjectEnvironmentCloneCaptureRequest struct {
	AccountID, ProjectID, SourceEnvironment, TargetEnvironment, IdempotencyKey string
}

// The root covers the current frozen configuration catalogue. Complete mode
// still requires coverage of the remaining resource kinds and a data checkpoint.
type ProjectEnvironmentCloneConfigurationCapture struct {
	OperationID, Hash      string
	Version, WorkloadCount int
}

type ProjectEnvironmentCloneConfigurationCaptureStore interface {
	CreateCapturedProjectEnvironmentCloneOperation(context.Context, ProjectEnvironmentCloneCaptureRequest) (ProjectEnvironmentCloneOperation, error)
	ProjectEnvironmentCloneConfigurationForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationCapture, error)
}

// Validation reads today's source in one control-plane snapshot and compares
// it with the immutable root. It is not a persistent configuration write fence
// or a common data checkpoint: later source edits still require coordination.
type ProjectEnvironmentCloneConfigurationValidationStore interface {
	ValidateProjectEnvironmentCloneSourceConfigurationForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationCapture, error)
}

type projectCloneConfigurationRoot struct {
	Version                 int                                 `json:"version"`
	AccountID               string                              `json:"account_id"`
	ProjectID               string                              `json:"project_id"`
	SourceEnvironment       string                              `json:"source_environment"`
	SourceReleaseSetID      string                              `json:"source_release_set_id"`
	ProjectConfigHash       string                              `json:"project_config_hash"`
	ProjectConfigValuesHash string                              `json:"project_config_values_hash"`
	FeatureFlagsHash        string                              `json:"feature_flags_hash,omitempty"`
	Workloads               []projectCloneConfigurationWorkload `json:"workloads"`
}

type projectCloneConfigurationWorkload struct {
	AppID        string `json:"app_id"`
	Name         string `json:"name"`
	Scope        string `json:"scope"`
	DeploymentID string `json:"deployment_id"`
	SnapshotHash string `json:"snapshot_hash"`
	SettingsHash string `json:"settings_hash"`
	ValuesHash   string `json:"values_hash"`
	BindingsHash string `json:"bindings_hash"`
	PoliciesHash string `json:"policies_hash"`
}

func validateCloneCaptureRequest(request ProjectEnvironmentCloneCaptureRequest) error {
	if !validCloneCredentialSourceID(request.AccountID) || !validCloneCredentialSourceID(request.ProjectID) ||
		!api.ValidProjectEnvironmentSlug(request.SourceEnvironment) || !api.ValidProjectEnvironmentSlug(request.TargetEnvironment) ||
		request.SourceEnvironment == request.TargetEnvironment || len(request.IdempotencyKey) < 1 || len(request.IdempotencyKey) > 255 {
		return ErrInvalidProjectEnvironmentCloneOperation
	}
	return nil
}

func cloneConfigurationRoot(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) (ProjectEnvironmentCloneConfigurationCapture, []byte, error) {
	config, err := capturedCloneProjectConfig(records)
	if err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, nil, err
	}
	_, configValuesHash, err := api.NormalizeProjectEnvironmentConfig(config.Values)
	if err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, nil, ErrConflict
	}
	root := projectCloneConfigurationRoot{Version: 1, AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: op.SourceEnvironment,
		SourceReleaseSetID: op.SourceReleaseSetID, ProjectConfigHash: config.Hash, ProjectConfigValuesHash: configValuesHash, FeatureFlagsHash: cloneFeatureFlagsHash(config.FeatureFlags),
		Workloads: make([]projectCloneConfigurationWorkload, 0, len(records))}
	apps, names := map[string]bool{}, map[string]bool{}
	for _, record := range records {
		record, err = copyCloneWorkloadRecord(record)
		if err != nil || record.OperationID != op.ID || apps[record.AppID] || names[record.WorkloadSlug] ||
			record.SourceValuesHash == "" || record.SourceBindingsHash == "" || record.SourcePoliciesHash == "" {
			return ProjectEnvironmentCloneConfigurationCapture{}, nil, ErrConflict
		}
		apps[record.AppID], names[record.WorkloadSlug] = true, true
		root.Workloads = append(root.Workloads, projectCloneConfigurationWorkload{AppID: record.AppID, Name: record.WorkloadSlug, Scope: record.SourceScope,
			DeploymentID: record.SourceDeploymentID, SnapshotHash: record.SourceHash, SettingsHash: record.SourceSettingsHash, ValuesHash: record.SourceValuesHash,
			BindingsHash: record.SourceBindingsHash, PoliciesHash: record.SourcePoliciesHash})
	}
	sort.Slice(root.Workloads, func(i, j int) bool { return root.Workloads[i].AppID < root.Workloads[j].AppID })
	raw, err := json.Marshal(root)
	if err != nil {
		return ProjectEnvironmentCloneConfigurationCapture{}, nil, err
	}
	hash := sha256.Sum256(raw)
	return ProjectEnvironmentCloneConfigurationCapture{OperationID: op.ID, Hash: hex.EncodeToString(hash[:]), Version: 1, WorkloadCount: len(root.Workloads)}, raw, nil
}

var _ ProjectEnvironmentCloneConfigurationCaptureStore = (*PgStore)(nil)
var _ ProjectEnvironmentCloneConfigurationValidationStore = (*PgStore)(nil)
