// Package previewconfig gives a PR preview the values and secrets its
// production app keeps in the pr-preview scope (ADR-20261028090000).
package previewconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is the part of state.Store a sync reads and writes.
type Store interface {
	AppBySlug(ctx context.Context, slug string) (state.App, error)
	ProjectEnvironmentBySlug(ctx context.Context, accountID, projectID, slug string) (state.ProjectEnvironment, error)
	ListAppEnvInScope(ctx context.Context, accountID, appID, scope string) ([]state.AppEnv, error)
	UpsertAppEnvInScope(ctx context.Context, accountID, appID, scope, key, value string) error
	ListAppSecretsInScope(ctx context.Context, accountID, appID, scope string) ([]state.AppSecret, error)
	UpsertAppSecretWithClassInScope(ctx context.Context, accountID, appID, scope, key, kid, valueHash, secretClass string, ciphertext []byte) error
}

// ErrNotPreview reports a sync between apps that are not a preview and its
// production app.
var ErrNotPreview = errors.New("previewconfig: not a preview of this app")

// Result counts the values and secrets a sync wrote. Skipped is set when the
// project has an environment named pr-preview: its values may be production
// configuration, so previews do not receive them.
type Result struct {
	Values, Secrets int
	Skipped         bool
}

// Changed reports whether the preview's configuration changed.
func (r Result) Changed() bool { return r.Values > 0 || r.Secrets > 0 }

// SyncFromParent syncs a PR preview from the production app it previews. An
// app that is not a PR preview is left alone.
func SyncFromParent(ctx context.Context, store Store, preview state.App) (Result, error) {
	if preview.PreviewOfSlug == "" || preview.PreviewPrNumber <= 0 {
		return Result{}, nil
	}
	parent, err := store.AppBySlug(ctx, preview.PreviewOfSlug)
	if err != nil {
		return Result{}, fmt.Errorf("previewconfig: load production app %s: %w", preview.PreviewOfSlug, err)
	}
	return Sync(ctx, store, parent, preview)
}

// Sync adds parent's pr-preview values and secrets to preview's default
// scope and updates the ones that differ. It removes nothing, and managed
// credentials are never copied. Secrets are copied sealed: sealing binds the
// account and key, not the app.
func Sync(ctx context.Context, store Store, parent, preview state.App) (Result, error) {
	if preview.ID == "" || preview.ID == parent.ID || preview.AccountID != parent.AccountID ||
		preview.PreviewOfSlug == "" || preview.PreviewOfSlug != parent.Slug {
		return Result{}, ErrNotPreview
	}
	if parent.ProjectID != "" {
		_, err := store.ProjectEnvironmentBySlug(ctx, parent.AccountID, parent.ProjectID, api.PreviewEnvScope)
		if err == nil {
			return Result{Skipped: true}, nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return Result{}, fmt.Errorf("previewconfig: check reserved environment: %w", err)
		}
	}
	values, err := syncValues(ctx, store, parent, preview)
	if err != nil {
		return Result{}, err
	}
	secrets, err := syncSecrets(ctx, store, parent, preview)
	if err != nil {
		return Result{Values: values}, err
	}
	return Result{Values: values, Secrets: secrets}, nil
}

func syncValues(ctx context.Context, store Store, parent, preview state.App) (int, error) {
	source, err := store.ListAppEnvInScope(ctx, parent.AccountID, parent.ID, api.PreviewEnvScope)
	if err != nil {
		return 0, fmt.Errorf("previewconfig: read preview values: %w", err)
	}
	if len(source) == 0 {
		return 0, nil
	}
	current, err := store.ListAppEnvInScope(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope)
	if err != nil {
		return 0, fmt.Errorf("previewconfig: read preview app values: %w", err)
	}
	have := make(map[string]string, len(current))
	for _, row := range current {
		have[row.Key] = row.Value
	}
	written := 0
	for _, row := range source {
		if value, ok := have[row.Key]; ok && value == row.Value {
			continue
		}
		if err := store.UpsertAppEnvInScope(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope, row.Key, row.Value); err != nil {
			return written, fmt.Errorf("previewconfig: write value %s: %w", row.Key, err)
		}
		written++
	}
	return written, nil
}

func syncSecrets(ctx context.Context, store Store, parent, preview state.App) (int, error) {
	source, err := store.ListAppSecretsInScope(ctx, parent.AccountID, parent.ID, api.PreviewEnvScope)
	if err != nil {
		return 0, fmt.Errorf("previewconfig: read preview secrets: %w", err)
	}
	if len(source) == 0 {
		return 0, nil
	}
	current, err := store.ListAppSecretsInScope(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope)
	if err != nil {
		return 0, fmt.Errorf("previewconfig: read preview app secrets: %w", err)
	}
	have := make(map[string]state.AppSecret, len(current))
	for _, secret := range current {
		have[secret.Key] = secret
	}
	written := 0
	for _, secret := range source {
		if managedSecret(secret) {
			continue
		}
		if existing, ok := have[secret.Key]; ok {
			if managedSecret(existing) || bytes.Equal(existing.Ciphertext, secret.Ciphertext) && existing.SecretClass == secret.SecretClass {
				continue
			}
		}
		if err := store.UpsertAppSecretWithClassInScope(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope,
			secret.Key, secret.Kid, secret.ValueHash, secret.SecretClass, secret.Ciphertext); err != nil {
			return written, fmt.Errorf("previewconfig: write secret %s: %w", secret.Key, err)
		}
		written++
	}
	return written, nil
}

// managedSecret reports a credential a platform owner manages: a managed
// PostgreSQL or object storage binding. Those never move between apps.
func managedSecret(secret state.AppSecret) bool {
	return secret.ManagedPostgresBindingID != "" || secret.ManagedCredentialRef != "" || secret.ManagedObjectStorageCredentialID != ""
}
