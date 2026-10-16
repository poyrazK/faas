package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/google/uuid"
)

type ProjectReleaseSecretConfiguration struct {
	Versions         map[string]map[string]string `json:"versions"`
	PreviousVersions map[string]map[string]string `json:"previous_versions"`
}

type ProjectReleaseSecretStageRequest struct {
	RequestID string                       `json:"request_id"`
	Workloads map[string]map[string]string `json:"workloads"`
}

type ProjectReleaseSecretStageResponse struct {
	RequestID string                       `json:"request_id"`
	Versions  map[string]map[string]string `json:"versions"`
}

func ValidReleaseSecretVersionID(id string) bool {
	value, err := uuid.Parse(id)
	return err == nil && value != uuid.Nil && value.String() == id
}

// DecodeProjectReleaseSecrets never reports decoder text or customer values.
func DecodeProjectReleaseSecrets(reader io.Reader, limits Limits) (map[string]map[string]string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, ProjectReleaseSecretStageMaxBytes+1))
	if err != nil || len(raw) > ProjectReleaseSecretStageMaxBytes {
		return nil, fmt.Errorf("release secret file exceeds the supported size")
	}
	var workloads map[string]map[string]string
	if json.Unmarshal(raw, &workloads) != nil || len(workloads) == 0 {
		return nil, fmt.Errorf("release secrets require workload names and secret value maps")
	}
	for name, values := range workloads {
		if !ValidAppSlug(name) || values == nil || len(values) > limits.SecretCountMax {
			return nil, fmt.Errorf("release secret snapshot exceeds workload or secret limits")
		}
		for key, value := range values {
			if ValidateSecretKey(key) != nil || value == "" || len(value) > limits.SecretValueMaxBytes {
				return nil, fmt.Errorf("release secret snapshot contains an invalid name or value size")
			}
		}
	}
	return workloads, nil
}

func (c *Client) StageProjectReleaseSecrets(ctx context.Context, slug string, req ProjectReleaseSecretStageRequest) (ProjectReleaseSecretStageResponse, error) {
	var out ProjectReleaseSecretStageResponse
	if !ValidProjectSlug(slug) || !ValidDeploymentAttemptID(req.RequestID) {
		return out, fmt.Errorf("invalid release secret staging identity")
	}
	return out, c.do(ctx, "POST", "/v1/projects/"+slug+"/release-secrets", req, &out)
}
