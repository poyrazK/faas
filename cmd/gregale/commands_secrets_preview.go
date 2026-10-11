package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This projection contains key names and requested options, never secret values.
type secretsPreview struct {
	DryRun      bool     `json:"dry_run"`
	Operation   string   `json:"operation"`
	Profile     string   `json:"profile"`
	App         string   `json:"app_slug"`
	Scope       string   `json:"scope"`
	AppSource   string   `json:"app_source"`
	ScopeSource string   `json:"scope_source"`
	Keys        []string `json:"keys"`
	Class       string   `json:"class,omitempty"`
	Restart     bool     `json:"restart_requested"`
	WaitForAck  bool     `json:"wait_for_ack_requested"`
	Timeout     string   `json:"timeout,omitempty"`
	Validation  string   `json:"validation"`
}

func previewSecrets(operation, app, scope, appSource, scopeSource string, keys []string, class string, restart, wait bool, timeout time.Duration) int {
	if !api.ValidAppSlug(app) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	if problem := api.ValidateScope(scopeOrDefault(scope)); problem != nil {
		return printErr("Invalid scope", errors.New(problem.Detail))
	}
	for _, key := range keys {
		if problem := api.ValidateSecretKey(key); problem != nil {
			return printErr("Invalid secret key", errors.New(problem.Detail))
		}
	}
	preview := secretsPreview{DryRun: true, Operation: operation, Profile: currentProfile(), App: app, Scope: scopeOrDefault(scope), AppSource: appSource, ScopeSource: scopeSource, Keys: keys, Class: class, Restart: restart, WaitForAck: wait, Validation: "local input only; remote existence, permissions, quotas, and runtime delivery are not checked"}
	if wait {
		preview.Timeout = timeout.String()
	}
	if jsonOutput {
		return jsonOut(writeJSON(preview))
	}
	printMutationTarget(app, scope, appSource, scopeSource)
	_, _ = fmt.Fprintf(osStdout, "Dry run: would %s the following secret keys:\n", operation)
	for _, key := range keys {
		_, _ = fmt.Fprintf(osStdout, "  %s\n", key)
	}
	if class != "" {
		_, _ = fmt.Fprintf(osStdout, "Snapshot class: %s\n", class)
	}
	_, _ = fmt.Fprintf(osStdout, "Restart requested: %t; acknowledgement wait requested: %t\n", restart, wait)
	if wait {
		_, _ = fmt.Fprintf(osStdout, "Wait timeout: %s\n", preview.Timeout)
	}
	_, _ = fmt.Fprintf(osStdout, "Validation: %s. No API requests or changes were made.\n", preview.Validation)
	return 0
}
