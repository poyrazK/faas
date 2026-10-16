package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"github.com/onebox-faas/faas/pkg/api"
)

func previewProjectPromotionWithSecrets(ctx context.Context, client *Client, project, from, to string, syncConfig, verify, migrate, workers bool, file, requestID string) (api.ProjectEnvironmentPromotionPreviewResponse, error) {
	if file == "" {
		return client.GetProjectEnvironmentPromotionPreviewWithMigration(ctx, project, to, from, syncConfig, verify, migrate, workers)
	}
	var out api.ProjectEnvironmentPromotionPreviewResponse
	values, err := readProjectReleaseSecrets(file)
	if err != nil {
		return out, err
	}
	raw, _, err := stageProjectReleaseSecretConfig(ctx, client, project, requestID, values, "")
	if err != nil {
		return out, err
	}
	var config api.ProjectReleaseConfigRequest
	if json.Unmarshal(raw, &config) != nil {
		return out, fmt.Errorf("invalid staged secret configuration")
	}
	out, err = client.PreviewProjectEnvironmentPromotionSecrets(ctx, project, to, api.ProjectEnvironmentPromotionSecretPreviewRequest{FromEnvironment: from, SyncConfig: syncConfig, Verify: verify, Secrets: config.Secrets})
	if err != nil {
		return out, err
	}
	if out.ProjectSlug != project || out.FromEnvironment != from || out.ToEnvironment != to || !out.SyncConfig || out.Secrets == nil {
		return out, fmt.Errorf("server omitted the requested production secret contract; no promotion was submitted")
	}
	for name, refs := range config.Secrets {
		actual, ok := out.Secrets.Versions[name]
		if !ok || !maps.Equal(actual, refs) {
			return out, fmt.Errorf("server secret version acknowledgement differs; no promotion was submitted")
		}
	}
	if out.CanPromote && out.Health == nil {
		return out, fmt.Errorf("server omitted required destination verification; no promotion was submitted")
	}
	return out, nil
}

func renderPromotionSecretChanges(changes []api.ProjectEnvironmentPromotionChange) {
	for _, workload := range changes {
		for _, change := range workload.SecretChanges {
			_, _ = fmt.Fprintf(osStdout, "  Secret %s/%s: %s\n", workload.WorkloadName, change.Key, change.Kind)
		}
	}
}
