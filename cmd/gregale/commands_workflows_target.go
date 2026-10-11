package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func resolveWorkflowApp(explicit string) (string, error) {
	app, err := resolveAppFlagOrContext(explicit)
	if err != nil {
		return "", fmt.Errorf("pass --app <slug> or link this checkout with gregale link <project> --app <slug>: %w", err)
	}
	if !api.ValidAppSlug(app) {
		return "", fmt.Errorf("pass a valid app slug with --app or correct the linked app")
	}
	return app, nil
}

func printWorkflowRunTarget(app, source string) {
	if jsonOutput {
		return
	}
	profileSource := "selected connection profile"
	if profileOverride != "" {
		profileSource = "explicit --profile"
	}
	_, _ = fmt.Fprintf(osStderr, "Workflow run target: profile=%s (%s); app=%s (%s)\n", compactInspectFailureText(currentProfile()), profileSource, compactInspectFailureText(app), source)
}
