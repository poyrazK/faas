package main

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// requiredSecretPreflightTimeout bounds the two metadata reads. The check is
// advisory, so a slow API must not hold up the deploy.
const requiredSecretPreflightTimeout = 10 * time.Second

// warnMissingRequiredSecrets compares the project's required-secret
// declaration (.gregale/required-secrets.json) with the app's secrets and
// environment variables in scope and warns before the upload when a name is
// missing. Without it a deploy missing DATABASE_URL built for minutes and
// then failed its startup as app_startup_crash (deploy-path review P10).
// It only warns: a value can also reach the app another way (a binding or
// the image's own env), and a failed read never blocks the deploy.
//
// The deploy calls it once the app exists and --secrets-file values are
// sealed, for a first deploy too (that is when a name is most often missing),
// and reads the declaration of the deployed source directory, not the shell's
// (prod test 2026-11-15: it ran only for `gregale dev` and helper deploys).
func warnMissingRequiredSecrets(ctx context.Context, client *Client, slug, scope, sourceDir string) {
	file, err := requiredSecretFileForSource(sourceDir)
	if err != nil || file == "" {
		return
	}
	required, err := readRequiredSecretKeys(file)
	if err != nil || len(required) == 0 {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, requiredSecretPreflightTimeout)
	defer cancel()
	secrets, err := client.ListSecretsWithScope(readCtx, slug, scope)
	if err != nil {
		return
	}
	env, err := client.GetAppsSlugEnvWithScope(readCtx, slug, scope)
	if err != nil {
		return
	}
	missing := missingRequiredSecretNames(required, scope, secrets, env)
	if len(missing) == 0 {
		return
	}
	PrintWarn(osStderr, "%s declares required names that %s does not have in scope %s: %s. The app may fail to start; set them with `gregale env set --app %s KEY=VALUE` or check with `gregale env check --app %s`.",
		oneLine(file), oneLine(slug), oneLine(scopeOrDefault(scope)), strings.Join(missing, ", "), oneLine(slug), oneLine(slug))
}

// missingRequiredSecretNames returns the required names that are neither a
// secret nor an environment variable of the app in scope, sorted.
func missingRequiredSecretNames(required []string, scope string, secrets api.AppSecretListResponse, env api.AppEnvListResponse) []string {
	scope = scopeOrDefault(scope)
	present := map[string]bool{}
	for _, row := range secrets.Secrets {
		if scopeOrDefault(row.Scope) == scope {
			present[row.Key] = true
		}
	}
	for _, row := range env.Env {
		present[row.Key] = true
	}
	var missing []string
	for _, key := range sortedUniqueStrings(required) {
		if !present[key] {
			missing = append(missing, key)
		}
	}
	return missing
}
