package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func secretsUnsetInteractive(app, scope string) int {
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() {
		return printErr("Interactive terminal required", errors.New("use secrets unset --app APP KEY --scope SCOPE for scripts"))
	}
	if !api.ValidAppSlug(app) {
		return printErr("Invalid app", errors.New("pass a valid app slug with --app"))
	}
	scope, err := resolveEnvironmentFlagOrContext(scope)
	if err != nil {
		return printErr("Could not read local project context", err)
	}
	// Pin an explicit scope so an empty value cannot become a cross-scope list.
	scope = scopeOrDefault(scope)
	if problem := api.ValidateScope(scope); problem != nil {
		return printErr("Invalid scope", errors.New(problem.Detail))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	list, err := client.ListSecretsWithScope(readCtx, app, scope)
	cancel()
	if err != nil {
		return printErr("Could not list secret names", err)
	}
	secrets := make([]api.AppSecretResponse, 0, len(list.Secrets))
	for _, secret := range list.Secrets {
		if scopeOrDefault(secret.Scope) == scope {
			if problem := api.ValidateSecretKey(secret.Key); problem != nil {
				return printErr("Invalid secret metadata", errors.New(problem.Detail))
			}
			secrets = append(secrets, secret)
		}
	}
	if len(secrets) == 0 {
		PrintProgress(osStdout, "App %s has no secrets in scope %s.", app, scope)
		return 0
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Key < secrets[j].Key })
	labels := make([]string, len(secrets))
	for i, secret := range secrets {
		labels[i] = secret.Key
	}
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, fmt.Sprintf("Choose a secret to remove from app %s, scope %s (names only).", app, scope), labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	selected := secrets[choice]
	_, _ = fmt.Fprintln(prompt.writer, "A fresh restart applies removal to running instances. Restart affects the app; without it, instances keep their boot environment until the next cold wake.")
	restart, err := prompt.confirm(ctx, "Request a fresh restart after removal?")
	if err != nil {
		return startInputExit(err)
	}
	wait, err := prompt.confirm(ctx, "Wait for runtime removal acknowledgement (up to 2 minutes)?")
	if err != nil {
		return startInputExit(err)
	}
	PrintProgress(osStdout, "Remove secret: app=%s; scope=%s; key=%s; restart=%t; wait-for-ack=%t", app, scope, selected.Key, restart, wait)
	confirmed, err := prompt.confirm(ctx, "Remove this secret?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "No secret removed.")
		return 0
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latest, err := client.ListSecretsWithScope(readCtx, app, scope)
	cancel()
	if err != nil {
		return printErr("Could not recheck selected secret", err)
	}
	current, found := findSecretStatus(latest, selected.Key, scope)
	if !found || current.UpdatedAt != selected.UpdatedAt || current.DeliveryVersion != selected.DeliveryVersion {
		return printErr("Selected secret changed", errors.New("run secrets unset --interactive again to review the current secret"))
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	return executeSecretsUnset(ctx, client, app, selected.Key, scope, restart, wait, 2*time.Minute)
}
