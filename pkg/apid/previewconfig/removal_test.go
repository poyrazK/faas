package previewconfig_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/previewconfig"
)

// ADR-20261103180000: removing a pr-preview entry from the production app
// finds the previews that still hold the copy a sync gave them, and leaves a
// preview whose value was changed on the preview itself.
func TestPreviewsHoldingFindsUnchangedCopies(t *testing.T) {
	f := newPreviewFixture(t)
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.store.UpsertAppEnvInScope(ctx, f.parent.AccountID, f.parent.ID, api.PreviewEnvScope, "STRIPE_KEY", "sk_test_1"))
	must(f.store.UpsertAppSecretWithClassInScope(ctx, f.parent.AccountID, f.parent.ID, api.PreviewEnvScope,
		"TOKEN", "kid", "hash", "", []byte("sealed-1")))
	if _, err := previewconfig.Sync(ctx, f.store, f.parent, f.preview); err != nil {
		t.Fatal(err)
	}

	value, ok, err := previewconfig.PendingRemoval(ctx, f.store, f.parent, "STRIPE_KEY", false)
	if err != nil || !ok || value.Value != "sk_test_1" {
		t.Fatalf("pending value removal = (%+v, %v, %v)", value, ok, err)
	}
	secret, ok, err := previewconfig.PendingRemoval(ctx, f.store, f.parent, "TOKEN", true)
	if err != nil || !ok || string(secret.Ciphertext) != "sealed-1" {
		t.Fatalf("pending secret removal = (%+v, %v, %v)", secret, ok, err)
	}
	if _, ok, err := previewconfig.PendingRemoval(ctx, f.store, f.parent, "MISSING", false); err != nil || ok {
		t.Fatalf("missing entry = (%v, %v)", ok, err)
	}

	for _, removal := range []previewconfig.Removal{value, secret} {
		holding, err := previewconfig.PreviewsHolding(ctx, f.store, f.parent, removal)
		if err != nil || len(holding) != 1 || holding[0].ID != f.preview.ID {
			t.Fatalf("holding %s = (%+v, %v)", removal.Key, holding, err)
		}
	}

	// A value set on the preview itself is not the production app's copy.
	must(f.store.UpsertAppEnvInScope(ctx, f.preview.AccountID, f.preview.ID, api.DefaultEnvScope, "STRIPE_KEY", "sk_test_mine"))
	if holding, err := previewconfig.PreviewsHolding(ctx, f.store, f.parent, value); err != nil || len(holding) != 0 {
		t.Fatalf("changed copy = (%+v, %v), want none", holding, err)
	}
}
