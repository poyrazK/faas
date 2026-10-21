package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ProjectReleaseConfiguration exposes only fingerprints and committed versions;
// configuration values remain server-side and are never saved in CLI journals.
type ProjectReleaseConfiguration struct {
	Secrets         *ProjectReleaseSecretConfiguration `json:"secrets,omitempty"`
	PreviousVersion int64                              `json:"previous_version"`
	PreviousHash    string                             `json:"previous_hash"`
	TargetHash      string                             `json:"target_hash"`
	EnvironmentHash string                             `json:"environment_hash"`
	RequestHash     string                             `json:"request_hash,omitempty"`
	TargetVersion   int64                              `json:"target_version,omitempty"`
	RestoredVersion int64                              `json:"restored_version,omitempty"`
}
type ProjectReleaseConfigRequest struct {
	Env     map[string]map[string]string `json:"env,omitempty"`
	Secrets map[string]map[string]string `json:"secrets,omitempty"`
}

// NormalizeProjectReleaseConfig validates workload-name -> complete plaintext
// variable sets. Each declared workload replaces its entire variable snapshot;
// omitted workloads inherit their prior release snapshot. Secrets use their
// existing separate delivery path.
func NormalizeProjectReleaseConfig(raw []byte) (json.RawMessage, string, error) {
	if len(raw) == 0 || len(raw) > ProjectReleaseConfigMaxBytes {
		return nil, "", fmt.Errorf("release config must be a JSON document within %d bytes", ProjectReleaseConfigMaxBytes)
	}
	var request ProjectReleaseConfigRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return nil, "", fmt.Errorf("release config must contain only an env object with workload variable maps")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(request.Env) == 0 && len(request.Secrets) == 0 {
		return nil, "", fmt.Errorf("release config must contain one env object with at least one workload")
	}
	for name, variables := range request.Env {
		if !ValidAppSlug(name) || variables == nil {
			return nil, "", fmt.Errorf("release config workload names must be valid and variable sets must be objects")
		}
		for key, value := range variables {
			if strings.ContainsRune(value, 0) {
				return nil, "", fmt.Errorf("release variables cannot contain NUL bytes")
			}
			if ValidateEnvKey(key) != nil || projectEnvironmentConfigKeyIsSensitive(key) {
				return nil, "", fmt.Errorf("release config contains an invalid or secret-reserved variable name")
			}
		}
	}
	for name, versions := range request.Secrets {
		if !ValidAppSlug(name) || versions == nil {
			return nil, "", fmt.Errorf("release secret snapshots must name existing workloads")
		}
		for key, version := range versions {
			if ValidateSecretKey(key) != nil || !ValidReleaseSecretVersionID(version) {
				return nil, "", fmt.Errorf("release secret references require valid names and version IDs")
			}
		}
	}
	canonical, err := json.Marshal(request)
	if err != nil {
		return nil, "", err
	}
	return canonical, fmt.Sprintf("%x", sha256.Sum256(canonical)), nil
}

type projectReleaseConfigContextKey struct{}

// ContextWithProjectReleaseConfig requests verified, paired code/configuration admission.
func ContextWithProjectReleaseConfig(ctx context.Context, raw json.RawMessage) context.Context {
	ctx = context.WithValue(ctx, projectReleaseConfigContextKey{}, append(json.RawMessage(nil), raw...))
	return context.WithValue(ctx, projectSafeReleaseContextKey{}, "atomic-config-verify")
}
func projectReleaseConfigFromContext(ctx context.Context) json.RawMessage {
	raw, _ := ctx.Value(projectReleaseConfigContextKey{}).(json.RawMessage)
	return raw
}
func ProjectReleaseConfigurationPolicyID(config *ProjectReleaseConfiguration) string {
	if config == nil {
		return ""
	}
	copy := *config
	copy.TargetVersion, copy.RestoredVersion = 0, 0
	raw, _ := json.Marshal(copy)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
