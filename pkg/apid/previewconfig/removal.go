package previewconfig

import (
	"bytes"
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Removal is a value or secret the production app is removing from its
// pr-preview scope, as it was before the removal.
type Removal struct {
	Key        string
	Secret     bool
	Value      string
	Ciphertext []byte
}

// HoldingStore is the part of state.Store PreviewsHolding reads.
type HoldingStore interface {
	ListAppEnvInScope(ctx context.Context, accountID, appID, scope string) ([]state.AppEnv, error)
	ListAppSecretsInScope(ctx context.Context, accountID, appID, scope string) ([]state.AppSecret, error)
	PreviewAppsByParent(ctx context.Context, accountID, parentSlug string) ([]state.App, error)
}

// PendingRemoval reads the pr-preview entry key of parent before it is
// removed. ok is false when parent has none, or it is a managed credential,
// which is never given to previews.
func PendingRemoval(ctx context.Context, store HoldingStore, parent state.App, key string, secret bool) (Removal, bool, error) {
	if secret {
		secrets, err := store.ListAppSecretsInScope(ctx, parent.AccountID, parent.ID, api.PreviewEnvScope)
		if err != nil {
			return Removal{}, false, fmt.Errorf("previewconfig: read preview secrets: %w", err)
		}
		for _, row := range secrets {
			if row.Key == key && !managedSecret(row) {
				return Removal{Key: key, Secret: true, Ciphertext: append([]byte(nil), row.Ciphertext...)}, true, nil
			}
		}
		return Removal{}, false, nil
	}
	values, err := store.ListAppEnvInScope(ctx, parent.AccountID, parent.ID, api.PreviewEnvScope)
	if err != nil {
		return Removal{}, false, fmt.Errorf("previewconfig: read preview values: %w", err)
	}
	for _, row := range values {
		if row.Key == key {
			return Removal{Key: key, Value: row.Value}, true, nil
		}
	}
	return Removal{}, false, nil
}

// PreviewsHolding returns parent's PR previews that still hold the copy a
// sync gave them of removal. A preview whose value was changed on the preview
// itself keeps it: that value is no longer the production app's
// (ADR-20261105090226).
func PreviewsHolding(ctx context.Context, store HoldingStore, parent state.App, removal Removal) ([]state.App, error) {
	previews, err := store.PreviewAppsByParent(ctx, parent.AccountID, parent.Slug)
	if err != nil {
		return nil, fmt.Errorf("previewconfig: list previews: %w", err)
	}
	var holding []state.App
	for _, preview := range previews {
		if !state.IsPRPreviewApp(preview) || preview.AccountID != parent.AccountID || preview.Status == state.AppDeleted {
			continue
		}
		held, err := holdsCopy(ctx, store, preview, removal)
		if err != nil {
			return holding, err
		}
		if held {
			holding = append(holding, preview)
		}
	}
	return holding, nil
}

func holdsCopy(ctx context.Context, store HoldingStore, preview state.App, removal Removal) (bool, error) {
	if removal.Secret {
		secrets, err := store.ListAppSecretsInScope(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope)
		if err != nil {
			return false, fmt.Errorf("previewconfig: read preview app secrets: %w", err)
		}
		for _, row := range secrets {
			if row.Key == removal.Key {
				return !managedSecret(row) && bytes.Equal(row.Ciphertext, removal.Ciphertext), nil
			}
		}
		return false, nil
	}
	values, err := store.ListAppEnvInScope(ctx, preview.AccountID, preview.ID, api.DefaultEnvScope)
	if err != nil {
		return false, fmt.Errorf("previewconfig: read preview app values: %w", err)
	}
	for _, row := range values {
		if row.Key == removal.Key {
			return row.Value == removal.Value, nil
		}
	}
	return false, nil
}
