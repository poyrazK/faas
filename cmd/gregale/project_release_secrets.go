package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func readProjectReleaseSecrets(path string) (map[string]map[string]string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return api.DecodeProjectReleaseSecrets(file, api.MustLimitsFor(api.PlanScale))
}
func stageProjectReleaseSecretConfig(ctx context.Context, client *Client, project, requestID string, workloads map[string]map[string]string, configFile string) (json.RawMessage, string, error) {
	if requestID == "" {
		requestID = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if !api.ValidDeploymentAttemptID(requestID) {
		return nil, "", fmt.Errorf("invalid release secret request ID")
	}
	_, _ = fmt.Fprintf(osStderr, "Release secret staging request: %s\n", requestID)
	response, err := client.StageProjectReleaseSecrets(ctx, project, api.ProjectReleaseSecretStageRequest{RequestID: requestID, Workloads: workloads})
	if err != nil {
		return nil, "", err
	}
	if response.RequestID != requestID || len(response.Versions) != len(workloads) {
		return nil, "", fmt.Errorf("release secret staging acknowledgement does not match")
	}
	for name, values := range workloads {
		refs, ok := response.Versions[name]
		if !ok || len(refs) != len(values) {
			return nil, "", fmt.Errorf("release secret staging acknowledgement does not match")
		}
		for key := range values {
			if !api.ValidReleaseSecretVersionID(refs[key]) {
				return nil, "", fmt.Errorf("release secret version acknowledgement is missing")
			}
		}
	}
	config := api.ProjectReleaseConfigRequest{Env: map[string]map[string]string{}, Secrets: response.Versions}
	if configFile != "" {
		raw, err := readProjectReleaseConfig(configFile)
		if err != nil {
			return nil, "", err
		}
		if json.Unmarshal(raw, &config) != nil {
			return nil, "", fmt.Errorf("invalid release configuration")
		}
		config.Secrets = response.Versions
	}
	raw, _ := json.Marshal(config)
	canonical, hash, err := api.NormalizeProjectReleaseConfig(raw)
	return canonical, hash, err
}
