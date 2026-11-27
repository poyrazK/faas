package previewdb

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type maskedPreviewDatabases struct {
	Databases
	source, preview managedpostgres.Database
	err             error
}

func (m maskedPreviewDatabases) List(context.Context, string) ([]managedpostgres.Database, error) {
	return []managedpostgres.Database{m.source}, nil
}

func (m maskedPreviewDatabases) EnsurePreviewDatabase(context.Context, managedpostgres.PreviewDatabaseRequest) (managedpostgres.Database, error) {
	return m.preview, m.err
}

type maskedPreviewBindings struct {
	Bindings
	created int
}

func (*maskedPreviewBindings) List(context.Context, string, string) ([]managedpostgres.Binding, error) {
	return []managedpostgres.Binding{{
		AppID: "prod", Scope: api.DefaultEnvScope, EnvironmentKey: "DATABASE_URL", State: managedpostgres.BindingStateReady,
	}}, nil
}

func (b *maskedPreviewBindings) CreateWithResult(_ context.Context, _ managedpostgres.CreateBindingRequest) (managedpostgres.Binding, bool, error) {
	b.created++
	return managedpostgres.Binding{}, false, nil
}

func TestMaskedPreviewRefusalsBlockDeploymentAndBinding(t *testing.T) {
	parent := state.App{ID: "prod", Slug: "shop", AccountID: "owner"}
	preview := state.App{ID: "pr", PreviewOfSlug: parent.Slug, PreviewPrNumber: 21, AccountID: parent.AccountID}
	for _, tc := range []struct {
		name     string
		setupErr error
		want     error
	}{
		{"failed validation", managedpostgres.ErrMaskedPreviewValidationFailed, managedpostgres.ErrMaskedPreviewValidationFailed},
		{"stale full copy", nil, managedpostgres.ErrPreviewReplacementRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings := &maskedPreviewBindings{}
			manager := Manager{Databases: maskedPreviewDatabases{
				source: managedpostgres.Database{ID: "production-db", Previews: managedpostgres.PreviewsMasked, State: managedpostgres.StateReady},
				preview: managedpostgres.Database{ID: "legacy-full-copy", RestoreSourceDatabaseID: "production-db",
					RestoreSourceResourceID: "physical-source", State: managedpostgres.StateReady},
				err: tc.setupErr,
			}, Bindings: bindings}
			err := manager.Prepare(t.Context(), api.PlanPro, parent, preview)
			var refused *RefusedError
			if !errors.As(err, &refused) || !errors.Is(err, tc.want) || bindings.created != 0 {
				t.Fatalf("expected safe refusal before binding: err=%v, bindings=%d", err, bindings.created)
			}
		})
	}
}
