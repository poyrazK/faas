// adr:1645 — verification pins reviewed source builds as well as OCI images.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// bindingVerificationArtifact is private evidence, never a substitute OCI
// digest. A source build is eligible only when its archive/build provenance
// agrees with the frozen reviewed inputs and a built rootfs exists.
func bindingVerificationArtifact(d Deployment) (string, error) {
	return bindingVerificationArtifactFor(d, false)
}

// Candidate qualification is the sole held-artifact consumer. Ordinary probes
// retain their live, unheld admission contract.
func bindingVerificationArtifactFor(d Deployment, candidate bool) (string, error) {
	if d.RootfsKey == "" || d.EnvironmentWorkloadHeld() && !candidate || candidate && (d.Status != DeploySnapshotting || !d.EnvironmentWorkloadHeld() || !d.EnvironmentWorkloadManaged()) {
		return "", ErrConflict
	}
	var frozen *EnvironmentWorkloadRuntime
	if d.EnvironmentWorkloadManaged() {
		var err error
		frozen, err = d.ScopedWorkloadRuntime()
		if err != nil || frozen == nil {
			return "", ErrConflict
		}
	}
	if d.ImageDigest == "" && (frozen == nil || frozen.SourceArchive == nil || d.RootfsBytes <= 0) {
		return "", ErrConflict
	}
	identity := struct {
		Version                                       int
		RootfsKey, ImageDigest                        string
		RootfsBytes                                   int64
		Kind                                          DeploymentKind
		BuildID, SourcePath, SourceSHA256, SourceRoot string
		SourceBytes                                   int64
		LogPath, SourceURL, CommitSHA, Handler        string
		Override                                      json.RawMessage
		Frozen                                        *EnvironmentWorkloadRuntime
	}{1, d.RootfsKey, d.ImageDigest, d.RootfsBytes, d.Kind,
		d.BuildID, d.SourcePath, d.SourceSHA256, d.SourceRoot, d.SourceBytes,
		d.LogPath, d.SourceURL, d.CommitSHA, d.Handler, deploymentInputJSON(d.OverrideEnv, "{}"), frozen}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", ErrConflict
	}
	raw, err = canonicalGitOpsValue(raw)
	if err != nil {
		return "", ErrConflict
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Decode the SQLC to_jsonb(deployments) projection once for every verification
// path. Deployment's public Go JSON shape deliberately omits frozen metadata.
func bindingVerificationDeploymentJSON(raw []byte) (Deployment, error) {
	var row struct {
		EnvironmentWorkloadDeploymentInputs
		ID           string           `json:"id"`
		AppID        string           `json:"app_id"`
		Scope        string           `json:"scope"`
		Status       DeploymentStatus `json:"status"`
		Kind         DeploymentKind   `json:"kind"`
		RootfsKey    string           `json:"rootfs_key"`
		RootfsBytes  int64            `json:"rootfs_bytes"`
		ImageDigest  string           `json:"image_digest"`
		BuildID      string           `json:"build_id"`
		SourcePath   string           `json:"source_path"`
		SourceSHA256 string           `json:"source_sha256"`
		SourceBytes  int64            `json:"source_bytes"`
		SourceRoot   string           `json:"source_root"`
		LogPath      string           `json:"log_path"`
		SourceURL    string           `json:"source_url"`
		CommitSHA    string           `json:"commit_sha"`
		Handler      string           `json:"handler"`
		Runtime      json.RawMessage  `json:"environment_workload_runtime"`
		Held         bool             `json:"environment_workload_held"`
	}
	if err := json.Unmarshal(raw, &row); err != nil {
		return Deployment{}, err
	}
	d := Deployment{ID: row.ID, AppID: row.AppID, Scope: row.Scope, Status: row.Status, Kind: row.Kind,
		RootfsKey: row.RootfsKey, RootfsBytes: row.RootfsBytes, ImageDigest: row.ImageDigest,
		BuildID: row.BuildID, SourcePath: row.SourcePath, SourceSHA256: row.SourceSHA256, SourceBytes: row.SourceBytes,
		SourceRoot: row.SourceRoot, LogPath: row.LogPath, SourceURL: row.SourceURL, CommitSHA: row.CommitSHA, Handler: row.Handler,
		EnvironmentWorkloadHeldValue: environmentWorkloadHeldFlag(row.Held)}
	if len(row.Runtime) > 0 && string(row.Runtime) != "null" {
		d.EnvironmentWorkloadRuntime = string(row.Runtime)
	}
	row.apply(&d)
	return d, nil
}
