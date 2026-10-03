package imaged

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fullRootfsCommandPATH follows guest-init's manifest < scoped API env < scoped
// secrets precedence without decrypting secrets or persisting runtime env.
// Unknown PATH defers only bare-command validation; explicit paths remain
// checkable. A failed env read cannot prove that the image command is invalid.
func fullRootfsCommandPATH(ctx context.Context, store state.Store, app state.App, dep state.Deployment, m api.AppManifest) *string {
	if len(m.Entrypoint) == 0 || strings.Contains(m.Entrypoint[0], "/") {
		return nil
	}
	if _, sealed := m.EnvSecrets["PATH"]; sealed {
		return nil
	}
	scope := dep.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	// With no explicit secret selection, wake injects every secret in scope.
	// With a nonempty selection, only the selected keys are injected.
	if len(m.EnvSecrets) == 0 {
		secrets, err := store.ListAppSecretsInScope(ctx, app.AccountID, app.ID, scope)
		if err != nil {
			return nil
		}
		for _, secret := range secrets {
			if secret.Key == "PATH" {
				return nil
			}
		}
	}
	rows, err := store.ListAppEnvInScope(ctx, app.AccountID, app.ID, scope)
	if err != nil {
		return nil
	}
	value := m.Env["PATH"]
	for _, row := range rows {
		if row.Key == "PATH" {
			value = row.Value
			break
		}
	}
	return &value
}
