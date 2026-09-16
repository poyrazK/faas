package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// resolvedManagedPostgresBinding is the provider-neutral deployment shape.
// The manifest names a logical database; only this seam resolves it to the
// managed-postgres service's stable ID before a binding is reserved.
type resolvedManagedPostgresBinding struct {
	database       managedpostgres.Database
	appSlug        string
	scope          string
	environmentKey string
	access         managedpostgres.CredentialAccess
}

// loadAndResolveManifestPostgresBindings validates the manifest and resolves
// every dependency before project/app mutation. Callers can therefore fail
// closed on a missing, ambiguous, or provisioning database without leaving a
// partially-created compute project behind.
func (s *server) loadAndResolveManifestPostgresBindings(
	ctx context.Context,
	acct state.Account,
	dir string,
	appSlugs []string,
	environment string,
) ([]resolvedManagedPostgresBinding, *api.Problem) {
	manifest, present, err := gregalemanifest.Load(dir)
	if err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", err.Error())
	}
	if !present || manifest == nil {
		return nil, nil
	}
	if prob := validateManifestAgainstPlan(manifest, acct.Plan); prob != nil {
		return nil, prob
	}
	if err := manifest.ValidateForPlan(acct.Plan); err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", err.Error())
	}
	if len(manifest.Databases) == 0 {
		return nil, nil
	}
	return s.resolveManifestPostgresBindings(ctx, acct, manifest, appSlugs, environment)
}

func (s *server) resolveManifestPostgresBindings(
	ctx context.Context,
	acct state.Account,
	manifest *gregalemanifest.Manifest,
	appSlugs []string,
	environment string,
) ([]resolvedManagedPostgresBinding, *api.Problem) {
	if manifest == nil || len(manifest.Databases) == 0 {
		return nil, nil
	}
	if s.managedPostgres == nil {
		return nil, managedPostgresManifestProblem(managedpostgres.ErrUnavailable,
			"managed PostgreSQL is not configured")
	}
	selected := make(map[string]struct{}, len(appSlugs))
	for _, slug := range appSlugs {
		slug = strings.ToLower(strings.TrimSpace(slug))
		if slug != "" {
			selected[slug] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", "database dependencies require at least one selected workload")
	}
	selectedSlugs := make([]string, 0, len(selected))
	for slug := range selected {
		selectedSlugs = append(selectedSlugs, slug)
	}
	sort.Strings(selectedSlugs)

	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return nil, managedPostgresManifestProblem(err, "could not list managed PostgreSQL databases")
	}
	environment = strings.TrimSpace(environment)
	seenTargets := make(map[string]struct{}, len(manifest.Databases))
	resolved := make([]resolvedManagedPostgresBinding, 0, len(manifest.Databases))
	var manifestScope string
	for i, dependency := range manifest.Databases {
		appSlug := strings.ToLower(strings.TrimSpace(dependency.App))
		if appSlug == "" {
			if len(selectedSlugs) != 1 {
				return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
					"Invalid manifest", fmt.Sprintf("database[%d]: app is required when deploying multiple workloads", i))
			}
			appSlug = selectedSlugs[0]
		} else if _, ok := selected[appSlug]; !ok {
			// A filtered project deploy only mutates selected workloads. Keep
			// declarations for other workloads dormant until that workload is
			// deployed; this matches the CLI's per-app filtering semantics.
			continue
		}

		scope := strings.TrimSpace(dependency.EffectiveScope())
		if environment != "" {
			if dependency.Scope != "" && scope != environment {
				return nil, api.NewProblem(http.StatusConflict, CodeAppManifestInvalid,
					"Manifest environment conflict", fmt.Sprintf("database[%d]: scope %q does not match deployment environment %q", i, scope, environment))
			}
			scope = environment
		}
		if problem := api.ValidateScope(scope); problem != nil {
			return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("database[%d]: scope: %s", i, problem.Detail))
		}
		if manifestScope == "" {
			manifestScope = scope
		} else if manifestScope != scope {
			return nil, api.NewProblem(http.StatusConflict, CodeAppManifestInvalid,
				"Manifest scope conflict", fmt.Sprintf("database[%d]: scope %q does not match the other deployment dependencies' scope %q", i, scope, manifestScope))
		}
		environmentKey := dependency.EffectiveEnvironmentKey()
		access := managedpostgres.CredentialAccess(dependency.EffectiveAccess())
		targetKey := strings.Join([]string{appSlug, scope, environmentKey}, "\x00")
		if _, duplicate := seenTargets[targetKey]; duplicate {
			return nil, api.NewProblem(http.StatusConflict, CodeAppManifestInvalid,
				"Manifest binding conflict", fmt.Sprintf("database[%d]: duplicate target (%s, %s, %s)", i, appSlug, scope, environmentKey))
		}
		seenTargets[targetKey] = struct{}{}

		database, resolveErr := resolveManagedPostgresDatabaseRecord(databases, dependency.Database)
		if resolveErr != nil {
			if strings.Contains(resolveErr.Error(), "ambiguous") {
				return nil, api.NewProblem(http.StatusConflict, "managed_postgres_ambiguous",
					"Managed PostgreSQL database reference is ambiguous", fmt.Sprintf("database[%d]: %v", i, resolveErr))
			}
			return nil, api.NewProblem(http.StatusNotFound, "managed_postgres_not_found",
				"Managed PostgreSQL database not found", fmt.Sprintf("database[%d]: no managed database named or identified %q", i, dependency.Database))
		}
		if database.State != managedpostgres.StateReady || database.ProviderResourceID == "" {
			return nil, api.NewProblem(http.StatusConflict, "managed_postgres_not_ready",
				"Managed PostgreSQL database is not ready", fmt.Sprintf("database %q is %s; retry after provisioning completes", database.Name, database.State))
		}
		resolved = append(resolved, resolvedManagedPostgresBinding{
			database: database, appSlug: appSlug, scope: scope,
			environmentKey: environmentKey, access: access,
		})
	}
	return resolved, nil
}

func resolveManagedPostgresDatabaseRecord(databases []managedpostgres.Database, reference string) (managedpostgres.Database, error) {
	reference = strings.TrimSpace(reference)
	var match managedpostgres.Database
	for _, database := range databases {
		if database.ID != reference && database.Name != reference {
			continue
		}
		if match.ID != "" && match.ID != database.ID {
			return managedpostgres.Database{}, fmt.Errorf("database reference %q is ambiguous; use its ID", reference)
		}
		match = database
	}
	if match.ID == "" {
		return managedpostgres.Database{}, fmt.Errorf("no database matches %q", reference)
	}
	return match, nil
}

// bindResolvedManagedPostgresBindings applies the resolved declarations after
// apps exist. It returns only newly-created binding IDs so a later dependency
// failure can be compensated without touching an idempotently reused binding.
func (s *server) bindResolvedManagedPostgresBindings(
	ctx context.Context,
	acct state.Account,
	bindings []resolvedManagedPostgresBinding,
	apps []state.App,
) ([]string, *api.Problem) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if s.managedPostgresBindings == nil {
		return nil, managedPostgresManifestProblem(managedpostgres.ErrUnavailable,
			"managed PostgreSQL bindings are not configured")
	}
	appIDs := make(map[string]string, len(apps))
	for _, app := range apps {
		appIDs[strings.ToLower(app.Slug)] = app.ID
	}
	created := make([]string, 0, len(bindings))
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			if _, err := s.managedPostgresBindings.Delete(context.WithoutCancel(ctx), acct.ID, created[i]); err != nil && !errors.Is(err, managedpostgres.ErrNotFound) {
				if s.log != nil {
					s.log.Warn("managed postgres binding rollback incomplete", "binding_id", created[i], "err", err)
				}
			}
		}
	}
	for i, dependency := range bindings {
		appID := appIDs[strings.ToLower(dependency.appSlug)]
		if appID == "" {
			rollback()
			return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("database dependency %d targets workload %q, but no deployed app exists", i+1, dependency.appSlug))
		}
		binding, wasCreated, err := s.managedPostgresBindings.CreateWithResult(ctx, managedpostgres.CreateBindingRequest{
			AccountID: acct.ID, DatabaseID: dependency.database.ID, AppID: appID,
			Scope: dependency.scope, EnvironmentKey: dependency.environmentKey, Access: dependency.access,
		})
		if wasCreated && binding.ID != "" {
			created = append(created, binding.ID)
		}
		if err != nil {
			rollback()
			return nil, managedPostgresManifestProblem(err, fmt.Sprintf("database dependency %d (%q): create binding", i+1, dependency.database.Name))
		}
		if binding.State != managedpostgres.BindingStateReady {
			rollback()
			return nil, api.NewProblem(http.StatusConflict, "managed_postgres_binding_not_ready",
				"Managed PostgreSQL binding is not ready", fmt.Sprintf("binding %q is %s; retry after provisioning completes", binding.ID, binding.State))
		}
	}
	return created, nil
}

func managedPostgresManifestProblem(err error, detail string) *api.Problem {
	status, code, title := http.StatusInternalServerError, "managed_postgres_error", "Managed PostgreSQL error"
	switch {
	case errors.Is(err, managedpostgres.ErrUnavailable):
		status, code, title = http.StatusServiceUnavailable, "managed_postgres_unavailable", "Managed PostgreSQL unavailable"
	case errors.Is(err, managedpostgres.ErrNotFound):
		status, code, title = http.StatusNotFound, "managed_postgres_not_found", "Managed PostgreSQL resource not found"
	case errors.Is(err, managedpostgres.ErrConflict):
		status, code, title = http.StatusConflict, "managed_postgres_conflict", "Managed PostgreSQL resource conflict"
	case errors.Is(err, managedpostgres.ErrInvalid):
		status, code, title = http.StatusBadRequest, "managed_postgres_invalid", "Invalid managed PostgreSQL request"
	}
	if detail == "" {
		detail = "The managed PostgreSQL operation could not be completed."
	}
	return api.NewProblem(status, code, title, detail)
}
