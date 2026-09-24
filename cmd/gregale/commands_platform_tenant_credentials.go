package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func platformTenantCredentialApplyCommand(ctx context.Context, client *api.Client, tenantID, path string, dryRun bool) int {
	body, err := os.ReadFile(path)
	if err != nil {
		return printErr("Read credential bundle failed", err)
	}
	var req api.ApplyPlatformTenantCredentialsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return printErr("Invalid credential bundle", err)
	}
	req.DryRun = req.DryRun || dryRun
	row, err := client.ApplyPlatformTenantCredentials(ctx, tenantID, req)
	if err != nil {
		return printErr("Credential apply failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(row))
	}
	for _, item := range row.Keys {
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", item.Action, item.ID, item.ConsumerID, item.Prefix); err != nil {
			return printErr("Output failed", err)
		}
	}
	return 0
}

func platformTenantCredentialListCommand(ctx context.Context, client *api.Client, tenantID string, limit, offset int) int {
	row, err := client.ListPlatformTenantCredentials(ctx, tenantID, limit, offset)
	if err != nil {
		return printErr("Credential list failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(row))
	}
	for _, item := range row.Keys {
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", item.ID, item.ConsumerID, item.Name, item.Prefix); err != nil {
			return printErr("Output failed", err)
		}
	}
	return 0
}
