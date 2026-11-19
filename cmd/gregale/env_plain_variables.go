package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// plainVariableLister reads an app's readable variables.
type plainVariableLister interface {
	GetAppsSlugEnvValues(ctx context.Context, slug, scope string) (api.AppEnvListResponse, error)
}

// printPlainVariables lists an app's plain (non-secret) variables with their
// values under the sealed secrets, so `gregale env list` shows imported
// Compose values and rewritten service addresses such as
// http://orders.svc.gregale:10081 (retail-store feedback B7, 2026-11-17).
// A failure to read them leaves the secret listing as it was.
func printPlainVariables(w io.Writer, client plainVariableLister, app, scope string, keys []string) {
	resp, err := client.GetAppsSlugEnvValues(context.Background(), app, scope)
	if err != nil {
		return
	}
	rows := make([]api.AppEnvResponse, 0, len(resp.Env))
	for _, row := range resp.Env {
		if len(keys) == 0 || slices.Contains(keys, row.Key) {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return
	}
	slices.SortFunc(rows, func(a, b api.AppEnvResponse) int { return strings.Compare(a.Key, b.Key) })
	_, _ = fmt.Fprintf(w, "\nVariables (plain, readable; %d/%d):\n", resp.Count, resp.Quota)
	for _, row := range rows {
		value := "(value not returned by this server)"
		if row.Value != nil {
			value = oneLine(*row.Value)
		}
		_, _ = fmt.Fprintf(w, "  %s=%s\n", row.Key, value)
	}
}

// setPlainVariables writes KEY=VALUE pairs as readable variables
// (`gregale env set --plain`).
func setPlainVariables(app, scope string, pairs []string) int {
	if len(pairs) == 0 {
		return printErr("Invalid --plain", errors.New("pass one or more KEY=VALUE pairs"))
	}
	values := make([][2]string, 0, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return printErr("Invalid variable", fmt.Errorf("%q is not KEY=VALUE", key))
		}
		if problem := api.ValidateEnvKey(key); problem != nil {
			return printErr("Invalid variable", errors.New(problem.Detail))
		}
		values = append(values, [2]string{key, value})
	}
	resolvedScope, err := resolveEnvironmentFlagOrContext(scope)
	if err != nil {
		return printErr("Could not read local project context", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	for _, kv := range values {
		if err := client.PutAppEnvInScope(context.Background(), app, resolvedScope, kv[0], kv[1]); err != nil {
			return printErr("Could not set "+kv[0], err)
		}
		if !jsonOutput {
			PrintOK(osStdout, "Set plain variable %s on %s; it applies to instances started from now on", kv[0], app)
		}
	}
	if jsonOutput {
		keys := make([]string, 0, len(values))
		for _, kv := range values {
			keys = append(keys, kv[0])
		}
		return jsonOut(writeJSON(map[string]any{"app": app, "scope": resolvedScope, "plain": keys}))
	}
	return 0
}
