package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// managedPostgresCatalogClient is the provider-neutral read surface needed to
// resolve a manifest reference. Keeping this seam above the SDK means deploy
// orchestration never imports Neon (or any other provider adapter).
type managedPostgresCatalogClient interface {
	ListManagedPostgresDatabases(context.Context) (api.ManagedPostgresDatabaseList, error)
}

type manifestPostgresClient interface {
	managedPostgresCatalogClient
	GetApp(context.Context, string) (api.AppResponse, error)
	CreateManagedPostgresBinding(context.Context, string, api.CreateManagedPostgresBindingRequest) (api.ManagedPostgresBinding, error)
}

// resolveManagedPostgresDatabase accepts either the stable API ID or the
// customer-facing logical name. Names are account-scoped and the API's list
// shape is intentionally safe to expose, so this avoids adding a provider-
// specific lookup route just for the CLI.
func resolveManagedPostgresDatabase(ctx context.Context, client managedPostgresCatalogClient, reference string) (api.ManagedPostgresDatabase, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return api.ManagedPostgresDatabase{}, fmt.Errorf("database reference is required")
	}
	response, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		return api.ManagedPostgresDatabase{}, err
	}
	var match api.ManagedPostgresDatabase
	for _, candidate := range response.Items {
		if candidate.ID != reference && candidate.Name != reference {
			continue
		}
		if match.ID != "" && candidate.ID != match.ID {
			return api.ManagedPostgresDatabase{}, fmt.Errorf("database reference %q is ambiguous; use its ID", reference)
		}
		match = candidate
	}
	if match.ID == "" {
		return api.ManagedPostgresDatabase{}, fmt.Errorf("no managed database named or identified %q", reference)
	}
	return match, nil
}

// deployManifestPostgresBindings turns declarative database dependencies into
// the existing durable app-binding saga. It runs after the app exists and
// before the deployment upload, so compute only starts with a ready sealed
// credential. Re-running deploy is safe because the API binding reservation
// is idempotent for the same app/database/scope/environment/access tuple.
func deployManifestPostgresBindings(ctx context.Context, client manifestPostgresClient, slug, cwd string) error {
	manifest, present, err := gregalemanifest.Load(cwd)
	if err != nil {
		return err
	}
	if !present || manifest == nil || len(manifest.Databases) == 0 {
		return nil
	}
	if err := manifest.Validate(); err != nil {
		return err
	}

	matching := make([]gregalemanifest.DatabaseDependency, 0, len(manifest.Databases))
	for _, dependency := range manifest.Databases {
		if dependency.App != "" && dependency.App != slug {
			continue
		}
		matching = append(matching, dependency)
	}
	if len(matching) == 0 {
		return nil
	}

	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("load app %q for database bindings: %w", slug, err)
	}
	databases, err := client.ListManagedPostgresDatabases(ctx)
	if err != nil {
		return fmt.Errorf("list managed databases for %q: %w", slug, err)
	}
	catalog := managedPostgresCatalog{items: databases.Items}
	for i, dependency := range matching {
		database, err := resolveManagedPostgresDatabase(ctx, catalog, dependency.Database)
		if err != nil {
			return fmt.Errorf("database dependency %d (%q): %w", i+1, dependency.Database, err)
		}
		binding, err := client.CreateManagedPostgresBinding(ctx, database.ID, api.CreateManagedPostgresBindingRequest{
			AppID:          app.ID,
			Scope:          dependency.EffectiveScope(),
			EnvironmentKey: dependency.EffectiveEnvironmentKey(),
			Access:         dependency.EffectiveAccess(),
		})
		if err != nil {
			return fmt.Errorf("database dependency %d (%q): create binding: %w", i+1, dependency.Database, err)
		}
		if binding.State != "ready" {
			return fmt.Errorf("database dependency %d (%q): binding %s is %s; retry after the database becomes ready", i+1, dependency.Database, binding.ID, binding.State)
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStdout, "  ✓ %s: database %s attached as %s\n", slug, database.Name, binding.EnvironmentKey)
		}
	}
	return nil
}

// manifestPostgresDeploymentScope returns the one scope that the current app's
// declarative database dependencies use. A deployment has exactly one env
// scope, so mixing scopes in one manifest would otherwise make one binding
// invisible to the resulting compute workload. The default scope is omitted
// on the wire to preserve the existing deployment behavior.
func manifestPostgresDeploymentScope(slug, cwd string) (string, error) {
	manifest, present, err := gregalemanifest.Load(cwd)
	if err != nil {
		return "", err
	}
	if !present || manifest == nil || len(manifest.Databases) == 0 {
		return "", nil
	}
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	var scope string
	for _, dependency := range manifest.Databases {
		if dependency.App != "" && dependency.App != slug {
			continue
		}
		current := dependency.EffectiveScope()
		if scope == "" {
			scope = current
			continue
		}
		if current != scope {
			return "", fmt.Errorf("database dependencies for app %q use multiple scopes (%q and %q); use one scope per deployment", slug, scope, current)
		}
	}
	if scope == api.DefaultEnvScope {
		return "", nil
	}
	return scope, nil
}

type managedPostgresCatalog struct {
	items []api.ManagedPostgresDatabase
}

func (c managedPostgresCatalog) ListManagedPostgresDatabases(context.Context) (api.ManagedPostgresDatabaseList, error) {
	return api.ManagedPostgresDatabaseList{Items: c.items}, nil
}
