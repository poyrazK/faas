package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func secretListHasRows(resp *api.AppSecretListResponse) bool {
	if len(resp.Secrets) > 0 {
		return true
	}
	for _, rows := range resp.SecretsByScope {
		if len(rows) > 0 {
			return true
		}
	}
	return false
}

func printEmptySecretList(app, scope, class, olderThan, deliveryStatus string, keys ...string) {
	scope = scopeOrDefault(scope)
	filters := []string{"scope=" + scope}
	if class != "" {
		filters = append(filters, "class="+class)
	}
	if olderThan != "" {
		filters = append(filters, "older-than="+olderThan)
	}
	if deliveryStatus != "" {
		filters = append(filters, "delivery-status="+deliveryStatus)
	}
	if len(keys) > 0 {
		filters = append(filters, "keys="+strings.Join(keys, ", "))
	}
	listCommand := fmt.Sprintf("%s secrets list --app %s --scope %s", recoveryCLI(), recoveryArgument(app), recoveryArgument(scope))
	filtered := class != "" || olderThan != "" || deliveryStatus != "" || len(keys) > 0
	if !filtered && scope != "__all__" {
		listCommand = fmt.Sprintf("%s secrets list --app %s --scope __all__", recoveryCLI(), recoveryArgument(app))
	}
	if filtered || scope != "__all__" {
		printEmptyResult("secret metadata for app "+app, strings.Join(filters, "; "), listCommand)
		if filtered {
			_, _ = fmt.Fprintln(osStdout, "The next command removes key, class, age, and delivery filters while keeping the selected scope.")
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "No secret metadata returned for app %s across all scopes.\n  Connection profile: %s\n", compactInspectFailureText(app), compactInspectFailureText(currentProfile()))
	}
	if len(keys) > 0 {
		_, _ = fmt.Fprintln(osStdout, "No matching key metadata was returned under these filters. This does not confirm removal from running applications.")
	}
	if olderThan != "" {
		_, _ = fmt.Fprintln(osStdout, "Age filtering excludes secrets with missing or invalid update timestamps.")
	}
	if !filtered {
		if scope == "__all__" {
			scope = "default"
			_, _ = fmt.Fprintln(osStdout, "To preview adding secrets, choose a write scope; this example uses default.")
		} else {
			_, _ = fmt.Fprintln(osStdout, "The next list checks other scopes. To preview adding secrets in the selected scope:")
		}
		_, _ = fmt.Fprintf(osStdout, "  %s env set --app %s --scope %s --from-stdin --dry-run < secrets.env\n", recoveryCLI(), recoveryArgument(app), recoveryArgument(scope))
		_, _ = fmt.Fprintln(osStdout, "Use your private KEY=VALUE file; the preview does not write values.")
	}
}
