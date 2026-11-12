package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func mcpNativeTaskNamespaceMatches(app api.AppResponse) bool {
	expected := strings.TrimSpace(os.Getenv(mcpTasksObserverNamespaceEnv))
	_, sealed := app.Manifest.EnvSecrets[mcpTasksObserverNamespaceEnv]
	return expected != "" && !sealed && strings.TrimSpace(app.Manifest.Env[mcpTasksObserverNamespaceEnv]) == expected
}

func mcpNativeWebTaskBindings(ctx context.Context, c *Client, p mcpNativeReleasePlan) error {
	app, err := c.GetApp(ctx, p.WebApp)
	if err != nil {
		return err
	}
	if !mcpNativeTaskNamespaceMatches(app) {
		return errors.New("web app must use the release's explicit MCP_TASK_NAMESPACE as a non-secret setting")
	}
	return nil
}
