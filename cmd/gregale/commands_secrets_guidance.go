package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderSecretReferenceAvailability(ctx context.Context, client *Client, app string, refs api.AppSecretReferenceListResponse) int {
	secrets, err := client.ListSecretsWithScope(ctx, app, refs.Environment)
	if err != nil {
		_, _ = fmt.Fprintln(osStdout, "\nSource availability: unknown; secret metadata could not be read.")
		return printErr("Could not check referenced secrets", err)
	}
	if len(secrets.SecretsByScope) > 0 {
		return printErr("Invalid secret scope", fmt.Errorf("server returned all-scope metadata instead of the requested environment"))
	}
	present := map[string]bool{}
	for _, secret := range secrets.Secrets {
		if scopeOrDefault(secret.Scope) != refs.Environment {
			return printErr("Invalid secret scope", fmt.Errorf("server returned secret metadata for a different environment"))
		}
		if api.ValidateSecretKey(secret.Key) != nil || present[secret.Key] {
			return printErr("Invalid secret metadata", fmt.Errorf("server returned invalid or duplicate secret keys"))
		}
		present[secret.Key] = true
	}
	keys := make([]string, 0, len(refs.References))
	for key := range refs.References {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	missing := map[string]bool{}
	_, _ = fmt.Fprintln(osStdout, "\nConfigured reference sources (metadata only):")
	for _, key := range keys {
		ref := refs.References[key]
		if (api.PutAppSecretReferenceRequest{Reference: ref}).Validate() != nil {
			return printErr("Invalid secret reference", fmt.Errorf("server returned an invalid reference"))
		}
		source := strings.TrimPrefix(ref, api.SecretRefPrefix)
		status := "present"
		if !present[source] {
			status = "missing"
			missing[source] = true
		}
		_, _ = fmt.Fprintf(osStdout, "  %s -> %s: %s in %s/%s\n", key, source, status, oneLine(app), oneLine(refs.Environment))
	}
	_, _ = fmt.Fprintln(osStdout, "This checks configured environment references only, not every secret required by application code or deployment manifests.")
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, name)
		}
		sort.Strings(names)
		_, _ = fmt.Fprintf(osStdout, "\nSupply these source keys: %s\n", strings.Join(names, ", "))
		_, _ = fmt.Fprintln(osStdout, "Use a private file containing KEY=VALUE lines; do not put values in the command itself.")
		_, _ = fmt.Fprintf(osStdout, "  %s env set --app %s --scope %s --from-stdin < secrets.env\n", recoveryCLI(), recoveryArgument(app), recoveryArgument(refs.Environment))
	}
	printSecretReferenceApplicationHint(app, refs.Environment)
	return 0
}

func printSecretReferenceApplicationHint(app, environment string) {
	_, _ = fmt.Fprintln(osStdout, "Reference changes apply on future cold wakes; running instances keep their current environment.")
	_, _ = fmt.Fprintf(osStdout, "Review scoped secret delivery (POSIX shells):\n  %s secrets list --app %s --scope %s\n", recoveryCLI(), recoveryArgument(app), recoveryArgument(environment))
	_, _ = fmt.Fprintln(osStdout, "A fresh restart affects the app's active workloads, not only the environment shown above. Review the serving deployment scope before requesting one.")
}

func printSecretUpdateNextSteps(app, scope, wakeID string, keys ...string) {
	command := fmt.Sprintf("%s secrets list --app %s --scope %s", recoveryCLI(), recoveryArgument(app), recoveryArgument(scopeOrDefault(scope)))
	for _, key := range sortedUniqueStrings(keys) {
		command += " --key " + recoveryArgument(key)
	}
	_, _ = fmt.Fprintf(osStdout, "\nApp: %s; scope: %s\nReview secret metadata and delivery (POSIX shells):\n  %s\n", oneLine(app), oneLine(scopeOrDefault(scope)), command)
	if wakeID != "" {
		_, _ = fmt.Fprintf(osStdout, "Follow the requested restart:\n  %s app %s restart status --wake-id %s --wait\n", recoveryCLI(), recoveryArgument(app), recoveryArgument(wakeID))
		return
	}
	_, _ = fmt.Fprintln(osStdout, "To apply on a fresh boot, review the serving deployment scope first. A restart affects the app's active workloads.")
	_, _ = fmt.Fprintf(osStdout, "  %s app %s restart --fresh --wait\n", recoveryCLI(), recoveryArgument(app))
}
