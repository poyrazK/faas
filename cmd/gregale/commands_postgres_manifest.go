package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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
	postgresEgressClient
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

// postgresDatabaseID passes a database ID through and resolves a logical
// name. get, delete, restore and bindings list sent names verbatim, so
// `postgres get orders` answered "invalid managed postgres request" while
// attach and resize accepted the same name (production-us hunt #9, H9-P108).
func postgresDatabaseID(ctx context.Context, client managedPostgresCatalogClient, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if _, err := uuid.Parse(reference); err == nil {
		return reference, nil
	}
	database, err := resolveManagedPostgresDatabase(ctx, client, reference)
	if err != nil {
		return "", err
	}
	return database.ID, nil
}

// deployManifestPostgresBindings turns declarative database dependencies into
// the existing durable app-binding saga. It runs after the app exists and
// before the deployment upload, so compute only starts with a ready sealed
// credential. Re-running deploy is safe because the API binding reservation
// is idempotent for the same app/database/scope/environment/access tuple.
func deployManifestPostgresBindings(ctx context.Context, client manifestPostgresClient, slug, cwd string, environments ...string) error {
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
	if readRefused(err) {
		// Attaching a database grants the app a credential, which takes
		// postgres:manage; a deploy token or a deploy:write key cannot
		// (ADR-20261031162858 amendment 1).
		return fmt.Errorf("gregale.yaml attaches databases to %q, which needs a credential with postgres:manage; a deploy token or a deploy:write key cannot attach them: %w", slug, err)
	}
	if err != nil {
		return fmt.Errorf("list managed databases for %q: %w", slug, err)
	}
	catalog := managedPostgresCatalog{items: databases.Items}
	environment := ""
	if len(environments) > 0 {
		environment = strings.TrimSpace(environments[0])
	}
	type bindingPlan struct {
		dependency gregalemanifest.DatabaseDependency
		database   api.ManagedPostgresDatabase
		scope      string
		create     string // name of a database this deploy creates first
	}
	// Every database the manifest creates, so an entry without `create` may
	// bind one another entry creates (ADR-20261106105015 §1).
	creates := map[string]gregalemanifest.DatabaseCreate{}
	for _, dependency := range manifest.Databases {
		if dependency.Create != nil {
			creates[strings.TrimSpace(dependency.Database)] = *dependency.Create
		}
	}
	// Resolve and validate every dependency before creating the first
	// database or binding. A typo or a still-provisioning database must not
	// leave an earlier dependency attached when the command is retried.
	plans := make([]bindingPlan, 0, len(matching))
	for i, dependency := range matching {
		name := strings.TrimSpace(dependency.Database)
		_, declaresCreate := creates[name]
		database, err := resolveManagedPostgresDatabase(ctx, catalog, dependency.Database)
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "ambiguous"):
				return fmt.Errorf("database dependency %d (%q): %w", i+1, dependency.Database, err)
			case !declaresCreate:
				return fmt.Errorf("database dependency %d (%q): %w; add `create: true` to create it on deploy", i+1, dependency.Database, err)
			}
		} else if database.State != "ready" && (!declaresCreate || database.State != "provisioning") {
			return fmt.Errorf("database dependency %d (%q): database is %s; retry after provisioning completes", i+1, dependency.Database, database.State)
		}
		scope, err := manifestDependencyScope(dependency, environment)
		if err != nil {
			return fmt.Errorf("database dependency %d (%q): %w", i+1, dependency.Database, err)
		}
		plan := bindingPlan{dependency: dependency, database: database, scope: scope}
		if database.ID == "" || database.State != "ready" {
			plan.create = name
		}
		plans = append(plans, plan)
	}
	// Missing databases are created; one still provisioning (an earlier
	// deploy's) is only waited for, never re-created with a spec computed now.
	pending := map[string]api.ManagedPostgresDatabase{}
	for _, plan := range plans {
		if plan.create != "" {
			pending[plan.create] = plan.database
		}
	}
	ready, err := ensureManifestDatabases(ctx, client, creates, pending)
	if err != nil {
		return err
	}
	for i := range plans {
		if plans[i].create != "" {
			plans[i].database = ready[plans[i].create]
		}
	}
	for i, plan := range plans {
		dependency := plan.dependency
		database := plan.database
		binding, err := client.CreateManagedPostgresBinding(ctx, database.ID, api.CreateManagedPostgresBindingRequest{
			AppID:          app.ID,
			Scope:          plan.scope,
			EnvironmentKey: dependency.EffectiveEnvironmentKey(),
			Access:         dependency.EffectiveAccess(),
		})
		if err != nil {
			return fmt.Errorf("database dependency %d (%q): create binding: %w", i+1, dependency.Database, err)
		}
		if binding.State != "ready" {
			if plan.create != "" && binding.State == "provisioning" {
				// The reconciler injects the key once the new database is
				// ready; the app reads it on its next start.
				PrintWarn(osStderr, "%s: database %s is still provisioning; %s arrives when it is ready and the app reads it on its next start", slug, database.Name, binding.EnvironmentKey)
				continue
			}
			return fmt.Errorf("database dependency %d (%q): binding %s is %s; retry after the database becomes ready", i+1, dependency.Database, binding.ID, binding.State)
		}
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStdout, "  ✓ %s: database %s attached as %s\n", slug, database.Name, binding.EnvironmentKey)
		}
	}
	ensurePostgresEgress(ctx, client, app)
	return nil
}

// manifestPostgresDeploymentScope returns the one scope that the current app's
// declarative database dependencies use. A deployment has exactly one env
// scope, so mixing scopes in one manifest would otherwise make one binding
// invisible to the resulting compute workload. The default scope is omitted
// on the wire to preserve the existing deployment behavior.
func manifestPostgresDeploymentScope(slug, cwd string, environments ...string) (string, error) {
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
	environment := ""
	if len(environments) > 0 {
		environment = strings.TrimSpace(environments[0])
	}
	var scope string
	for _, dependency := range manifest.Databases {
		if dependency.App != "" && dependency.App != slug {
			continue
		}
		current, scopeErr := manifestDependencyScope(dependency, environment)
		if scopeErr != nil {
			return "", scopeErr
		}
		if scope == "" {
			scope = current
			continue
		}
		if current != scope {
			return "", fmt.Errorf("database dependencies for app %q use multiple scopes (%q and %q); use one scope per deployment", slug, scope, current)
		}
	}
	if environment != "" || scope == api.DefaultEnvScope {
		return "", nil
	}
	return scope, nil
}

func manifestDependencyScope(dependency gregalemanifest.DatabaseDependency, environment string) (string, error) {
	return manifestScopeForDeclared(dependency.Scope, environment)
}

type managedPostgresCatalog struct {
	items []api.ManagedPostgresDatabase
}

func (c managedPostgresCatalog) ListManagedPostgresDatabases(context.Context) (api.ManagedPostgresDatabaseList, error) {
	return api.ManagedPostgresDatabaseList{Items: c.items}, nil
}

// manifestPostgresCreator is the optional client surface for `create`
// declarations; fakes without it refuse them.
type manifestPostgresCreator interface {
	GetManagedPostgresCapabilities(context.Context, string) (api.ManagedPostgresCapabilities, error)
	CreateManagedPostgresDatabase(context.Context, api.CreateManagedPostgresDatabaseRequest) (api.ManagedPostgresDatabase, error)
	GetManagedPostgresDatabase(context.Context, string) (api.ManagedPostgresDatabase, error)
}

// manifestDatabaseReadyWait bounds how long a deploy waits for a database
// it just created before binding it pending.
var (
	manifestDatabaseReadyWait = 3 * time.Minute
	manifestDatabasePoll      = 2 * time.Second
)

// ensureManifestDatabases creates each pending database that does not exist
// yet (zero ID) and waits for every pending one to become ready, so the
// deployment that follows starts with its key. It returns the latest view of
// each.
func ensureManifestDatabases(ctx context.Context, client any, creates map[string]gregalemanifest.DatabaseCreate, pending map[string]api.ManagedPostgresDatabase) (map[string]api.ManagedPostgresDatabase, error) {
	out := map[string]api.ManagedPostgresDatabase{}
	names := make([]string, 0, len(pending))
	for name := range pending {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		creator, ok := client.(manifestPostgresCreator)
		if !ok {
			return nil, fmt.Errorf("database %q: creating databases is unavailable", name)
		}
		database := pending[name]
		if database.ID != "" {
			if !jsonOutput {
				PrintProgress(osStderr, "Waiting for database %s, still provisioning", database.Name)
			}
		} else {
			created, err := createManifestDatabase(ctx, creator, name, creates[name])
			if err != nil {
				return nil, err
			}
			database = created
		}
		deadline := time.Now().Add(manifestDatabaseReadyWait)
		for database.State == "provisioning" && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(manifestDatabasePoll):
			}
			if latest, err := creator.GetManagedPostgresDatabase(ctx, database.ID); err == nil {
				database = latest
			}
		}
		if database.State != "ready" && database.State != "provisioning" {
			return nil, fmt.Errorf("database %q is %s", name, database.State)
		}
		out[name] = database
	}
	return out, nil
}

func createManifestDatabase(ctx context.Context, creator manifestPostgresCreator, name string, create gregalemanifest.DatabaseCreate) (api.ManagedPostgresDatabase, error) {
	request := create.Request(name)
	if request.Region == "" || request.PostgresMajor == 0 {
		capabilities, err := creator.GetManagedPostgresCapabilities(ctx, request.Region)
		if err != nil {
			return api.ManagedPostgresDatabase{}, fmt.Errorf("database %q: read managed PostgreSQL capabilities: %w", name, err)
		}
		if request.Region == "" {
			request.Region = capabilities.Region
		}
		if create.PostgresMajor == 0 {
			// The newest offered major, as a project deploy picks it.
			for _, major := range capabilities.PostgresMajors {
				request.PostgresMajor = max(request.PostgresMajor, major)
			}
		}
	}
	database, err := creator.CreateManagedPostgresDatabase(ctx, request)
	if readRefused(err) {
		return database, fmt.Errorf("gregale.yaml creates database %q, which needs a credential with postgres:manage: %w", name, err)
	}
	if err != nil {
		return database, fmt.Errorf("create database %q: %w", name, err)
	}
	if !jsonOutput {
		PrintProgress(osStderr, "Creating database %s (PostgreSQL %d, %s)", database.Name, database.PostgresMajor, database.Region)
	}
	return database, nil
}
