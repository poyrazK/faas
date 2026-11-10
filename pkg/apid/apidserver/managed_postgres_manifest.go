package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/managedpg"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
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
	// create is set when the database does not exist yet and the manifest
	// declares `create` for it (ADR-20261106105015 §1): apply creates it
	// before binding. database is then the zero value.
	create *manifestDatabaseCreation
	// pending accepts a binding whose database is still provisioning: the
	// binding reconciler injects the key once it is ready, as for Compose
	// postgres services (ADR-20261025111824).
	pending bool
}

// manifestDatabaseCreation is one database a deploy creates from gregale.yaml.
type manifestDatabaseCreation struct {
	name string
	spec managedpostgres.Spec
}

// databaseName names the bound database whether or not it exists yet.
func (b resolvedManagedPostgresBinding) databaseName() string {
	if b.create != nil {
		return b.create.name
	}
	return b.database.Name
}

// resolvedProjectManifest contains the declarations needed after project
// workloads have been reconciled. The async-route presence bit preserves the
// manifest distinction between an absent key (leave routes untouched) and an
// explicit empty list (clear routes for selected workloads).
type resolvedProjectManifest struct {
	PostgresBindings   []resolvedManagedPostgresBinding
	AsyncRoutes        []gregalemanifest.AsyncRoute
	AsyncRoutesPresent bool
	// Routes carry the same presence distinction (ADR-20261021223225).
	Routes        []gregalemanifest.PathRoute
	RoutesPresent bool
}

// loadAndResolveProjectManifest validates the manifest and resolves every
// database dependency before project/app mutation. It also returns async
// routes for the apply phase, which reconciles them only after selected apps
// exist.
func (s *server) loadAndResolveProjectManifest(
	ctx context.Context,
	acct state.Account,
	dir string,
	appSlugs []string,
	environment string,
) (resolvedProjectManifest, *api.Problem) {
	manifest, present, err := gregalemanifest.Load(dir)
	if err != nil {
		return resolvedProjectManifest{}, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", err.Error())
	}
	if !present || manifest == nil {
		return resolvedProjectManifest{}, nil
	}
	if _, shared := projectaccess.From(ctx); shared && manifest.AsyncRoutes != nil {
		return resolvedProjectManifest{}, api.NewProblem(http.StatusConflict, api.CodeConflict, "Organization async bindings unavailable", "deploy application workloads with environment configuration; async-route bindings require separate organization ownership support")
	}
	_, sharedProject := projectaccess.From(ctx)
	_, sharedApp := appaccess.FromContext(ctx)
	if len(manifest.Databases) > 0 && (sharedProject || sharedApp) {
		if p := postgresaccess.AuthorizeManifestBindings(ctx, loadOrgAuditFrom(s.audit)); p != nil {
			return resolvedProjectManifest{}, p
		}
	}
	if prob := validateManifestAgainstPlan(manifest, acct.Plan, s.agentStepsAllowed(acct)); prob != nil {
		return resolvedProjectManifest{}, prob
	}
	if err := manifest.ValidateForPlan(acct.Plan); err != nil {
		return resolvedProjectManifest{}, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", err.Error())
	}
	bindings, problem := s.resolveManifestPostgresBindings(ctx, acct, manifest, appSlugs, environment)
	if problem != nil {
		return resolvedProjectManifest{}, problem
	}
	return resolvedProjectManifest{
		PostgresBindings:   bindings,
		AsyncRoutes:        manifest.AsyncRoutes,
		AsyncRoutesPresent: manifest.AsyncRoutes != nil,
		Routes:             manifest.Routes,
		RoutesPresent:      manifest.Routes != nil,
	}, nil
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

	if managedpostgres.Organization(ctx) == "" {
		org, err := s.store.OrgByPersonalAccount(ctx, acct.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, api.ErrCapacity("could not resolve database ownership")
		}
		ctx = managedpostgres.WithOwnership(ctx, org.ID, acct.ID)
	}
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return nil, managedPostgresManifestProblem(err, "could not list managed PostgreSQL databases")
	}
	environment = strings.TrimSpace(environment)
	// Every database the manifest creates, by name, so a dependency without
	// `create` may bind a database another entry creates.
	creates := map[string]gregalemanifest.DatabaseCreate{}
	for _, dependency := range manifest.Databases {
		if dependency.Create != nil {
			creates[strings.TrimSpace(dependency.Database)] = *dependency.Create
		}
	}
	planned := map[string]*manifestDatabaseCreation{}
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
		subject := platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan), OrgID: managedpostgres.Organization(ctx)}
		// Application Auth is a dark launch: without it the mode is unknown.
		if access == managedpostgres.CredentialAuth && !s.applicationAuthEnabled(subject) {
			return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("database[%d]: access %q not in {read_write, read_only, migration, data_api, session}", i, access))
		}
		if problem := s.dataAPIAccessProblem(subject, access, fmt.Sprintf(`database[%d]: access "data_api"`, i)); problem != nil {
			return nil, problem
		}
		targetKey := strings.Join([]string{appSlug, scope, environmentKey}, "\x00")
		if _, duplicate := seenTargets[targetKey]; duplicate {
			return nil, api.NewProblem(http.StatusConflict, CodeAppManifestInvalid,
				"Manifest binding conflict", fmt.Sprintf("database[%d]: duplicate target (%s, %s, %s)", i, appSlug, scope, environmentKey))
		}
		seenTargets[targetKey] = struct{}{}

		name := strings.TrimSpace(dependency.Database)
		create, declaresCreate := creates[name]
		database, resolveErr := resolveManagedPostgresDatabaseRecord(databases, dependency.Database)
		if resolveErr != nil {
			if strings.Contains(resolveErr.Error(), "ambiguous") {
				return nil, api.NewProblem(http.StatusConflict, "managed_postgres_ambiguous",
					"Managed PostgreSQL database reference is ambiguous", fmt.Sprintf("database[%d]: %v", i, resolveErr))
			}
			if !declaresCreate {
				return nil, api.NewProblem(http.StatusNotFound, "managed_postgres_not_found",
					"Managed PostgreSQL database not found", fmt.Sprintf("database[%d]: no managed database named or identified %q; add `create: true` to create it on deploy", i, dependency.Database))
			}
			creation := planned[name]
			if creation == nil {
				spec, problem := s.manifestDatabaseCreateSpec(ctx, acct, create, len(databases)+len(planned), i)
				if problem != nil {
					return nil, problem
				}
				creation = &manifestDatabaseCreation{name: name, spec: spec}
				planned[name] = creation
			}
			resolved = append(resolved, resolvedManagedPostgresBinding{
				appSlug: appSlug, scope: scope, environmentKey: environmentKey, access: access,
				create: creation, pending: true,
			})
			continue
		}
		pending := false
		if database.State != managedpostgres.StateReady || database.ProviderResourceID == "" {
			// A database this manifest creates may still be provisioning
			// from an earlier deploy; its key arrives when it is ready.
			if !declaresCreate || database.State != managedpostgres.StateProvisioning {
				return nil, api.NewProblem(http.StatusConflict, "managed_postgres_not_ready",
					"Managed PostgreSQL database is not ready", fmt.Sprintf("database %q is %s; retry after provisioning completes", database.Name, database.State))
			}
			pending = true
		}
		resolved = append(resolved, resolvedManagedPostgresBinding{
			database: database, appSlug: appSlug, scope: scope,
			environmentKey: environmentKey, access: access, pending: pending,
		})
	}
	return resolved, nil
}

// manifestDatabaseCreateSpec checks, before any mutation, that the account
// may create the database a manifest declares, with the same gates as
// `gregale postgres create`; existing counts databases the account has plus
// the ones this deploy already plans.
func (s *server) manifestDatabaseCreateSpec(ctx context.Context, acct state.Account, create gregalemanifest.DatabaseCreate, existing, index int) (managedpostgres.Spec, *api.Problem) {
	detail := func(format string, a ...any) string {
		return fmt.Sprintf("database[%d]: create: ", index) + fmt.Sprintf(format, a...)
	}
	// Creating a database is `gregale postgres create`: postgres:manage on
	// every account, so a deploy token or deploy:write key (ADR-20261031162858
	// amendment 1), or a push deploy without a principal, cannot buy one.
	if sharedComposePostgres(ctx) && authorizeComposePostgres(ctx, nil) != nil ||
		!authmw.HasScope((&http.Request{}).WithContext(ctx), api.ScopesManagedPostgresManageSurface...) {
		return managedpostgres.Spec{}, api.NewProblem(http.StatusForbidden, "managed_postgres_forbidden",
			"Cannot create managed PostgreSQL database", detail("creating a database needs a credential with postgres:manage; deploy tokens and deploy:write keys can only bind existing databases"))
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 || managedPostgresFreeClosed(acct.Plan, s.managedPostgresCapMode) {
		return managedpostgres.Spec{}, managedPostgresManifestProblem(managedpostgres.ErrNotInPlan, detail("the plan does not include managed databases"))
	}
	discovery, err := s.managedPostgres.DiscoverCapabilities(ctx, acct.ID, strings.TrimSpace(create.Region))
	if err != nil {
		return managedpostgres.Spec{}, managedPostgresManifestProblem(err, detail("could not read managed PostgreSQL capabilities"))
	}
	if !discovery.ProvisioningEnabled {
		return managedpostgres.Spec{}, managedPostgresManifestProblem(managedpostgres.ErrUnavailable, detail("managed PostgreSQL provisioning is not open to this account"))
	}
	if room := min(limits.DatabasesMax, discovery.DatabaseLimit); existing >= room {
		return managedpostgres.Spec{}, managedPostgresManifestProblem(managedpostgres.ErrQuotaExceeded, detail("the plan's limit of %d databases is reached", room))
	}
	request := create.Request("")
	if request.Region == "" {
		request.Region = discovery.Region
	}
	if request.PostgresMajor == 0 {
		// The newest offered major, as for an untagged Compose postgres image
		// (ADR-20261025111824); the API's fixed default may not be offered.
		if major, offered := composePostgresMajor("postgres", discovery.Capabilities.PostgresMajors); offered {
			request.PostgresMajor = major
		}
	}
	spec, err := managedPostgresSpecFromRequest(request, limits)
	if err != nil {
		return managedpostgres.Spec{}, managedPostgresManifestProblem(err, detail("%v", err))
	}
	if err := discovery.Capabilities.Supports(spec); err != nil {
		return managedpostgres.Spec{}, managedPostgresManifestProblem(err, detail("%v", err))
	}
	return spec, nil
}

// manifestDatabasePlan is the plan view of the databases a manifest creates,
// so a reviewer sees them before apply (source gregale.yaml).
func manifestDatabasePlan(bindings []resolvedManagedPostgresBinding) []api.PlanManaged {
	var out []api.PlanManaged
	index := map[string]int{}
	for _, binding := range bindings {
		if binding.create == nil {
			continue
		}
		i, ok := index[binding.create.name]
		if !ok {
			i = len(out)
			index[binding.create.name] = i
			out = append(out, api.PlanManaged{Name: binding.create.name, Kind: "postgres", Source: "gregale.yaml",
				Provision: &api.PlanManagedProvision{Database: binding.create.name, PostgresMajor: binding.create.spec.PostgresMajor}})
		}
		out[i].Provision.Bindings = append(out[i].Provision.Bindings, api.PlanManagedBinding{Workload: binding.appSlug, EnvKey: binding.environmentKey})
	}
	return out
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
	// Every target app must exist before a database is created for it, so a
	// mistyped `app` cannot leave an unbound paid database behind.
	for i, dependency := range bindings {
		if appIDs[strings.ToLower(dependency.appSlug)] == "" {
			return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("database dependency %d targets workload %q, but no deployed app exists", i+1, dependency.appSlug))
		}
	}
	// Databases the manifest creates, once each, before their bindings. A
	// later failure leaves a created database in place: data is never
	// deleted implicitly, and the next deploy reuses it by name.
	databases := map[string]managedpostgres.Database{}
	for _, dependency := range bindings {
		if dependency.create == nil || databases[dependency.create.name].ID != "" {
			continue
		}
		if s.managedPostgres == nil {
			return nil, managedPostgresManifestProblem(managedpostgres.ErrUnavailable, "managed PostgreSQL is not configured")
		}
		ownedCtx, err := s.composePostgresOwnership(ctx, acct)
		if err != nil {
			return nil, api.ErrCapacity("could not resolve database ownership")
		}
		database, err := s.managedPostgres.Create(ownedCtx, managedpostgres.CreateRequest{AccountID: acct.ID, Name: dependency.create.name, Spec: dependency.create.spec})
		if err != nil {
			return nil, managedPostgresManifestProblem(err, fmt.Sprintf("gregale.yaml: create database %q", dependency.create.name))
		}
		databases[dependency.create.name] = database
		s.audit.Emit(ctx, "managed_postgres.manifest_provisioned", &acct.ID, map[string]any{
			"database_id": database.ID, "database": database.Name, "state": database.State,
		})
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
		database := dependency.database
		if dependency.create != nil {
			database = databases[dependency.create.name]
		}
		binding, wasCreated, err := s.managedPostgresBindings.CreateWithResult(ctx, managedpostgres.CreateBindingRequest{
			AccountID: acct.ID, DatabaseAccountID: database.AccountID, DatabaseID: database.ID, AppID: appID,
			Scope: dependency.scope, EnvironmentKey: dependency.environmentKey, Access: dependency.access,
		})
		if wasCreated && binding.ID != "" {
			created = append(created, binding.ID)
		}
		if err != nil {
			rollback()
			if errors.Is(err, managedpostgres.ErrConflict) {
				if problem := s.manifestBindingChangedProblem(ctx, acct.ID, appID, dependency, database.Name); problem != nil {
					return nil, problem
				}
			}
			return nil, managedPostgresManifestProblem(err, fmt.Sprintf("database dependency %d (%q): create binding", i+1, dependency.databaseName()))
		}
		if dependency.pending && binding.State == managedpostgres.BindingStateProvisioning {
			continue
		}
		if binding.State != managedpostgres.BindingStateReady {
			rollback()
			return nil, api.NewProblem(http.StatusConflict, "managed_postgres_binding_not_ready",
				"Managed PostgreSQL binding is not ready", fmt.Sprintf("binding %q is %s; retry after provisioning completes", binding.ID, binding.State))
		}
	}
	return created, nil
}

// manifestBindingChangedProblem explains a gregale.yaml database entry whose
// environment key already belongs to a binding of another database or access
// mode (ADR-20261110120000). A deploy never replaces such a binding: that
// would revoke the credential the serving release uses. Nil when the conflict
// is something else.
func (s *server) manifestBindingChangedProblem(ctx context.Context, accountID, appID string, dependency resolvedManagedPostgresBinding, databaseName string) *api.Problem {
	bindings, err := s.managedPostgresBindings.ListForApp(ctx, accountID, appID, dependency.scope)
	if err != nil {
		// The caller reports the original conflict instead.
		return nil //nolint:nilerr // explanation is best-effort
	}
	for _, existing := range bindings {
		if existing.EnvironmentKey != dependency.environmentKey || existing.Scope != dependency.scope ||
			existing.State == string(managedpostgres.BindingStateDeleted) {
			continue
		}
		if existing.DatabaseName == databaseName && existing.Access == string(dependency.access) {
			return nil
		}
		return api.NewProblem(http.StatusConflict, "managed_postgres_binding_changed", "gregale.yaml changes an existing binding",
			fmt.Sprintf("%s in scope %q is bound to database %q with %s access; gregale.yaml asks for database %q with %s access. A deploy does not replace a binding, because that would revoke the credential the serving release uses.",
				dependency.environmentKey, dependency.scope, existing.DatabaseName, existing.Access, databaseName, dependency.access)).
			WithHint("Bind the new database or access under another env key and switch the app to it, or delete the old binding (gregale postgres bindings) and deploy again.").
			WithDocs("https://gregale.dev/docs/managed-postgres")
	}
	return nil
}

func managedPostgresManifestProblem(err error, detail string) *api.Problem {
	if problem := managedpg.LimitProblem(err); problem != nil {
		return problem
	}
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
	case errors.Is(err, managedpostgres.ErrUnsupported):
		status, code, title = http.StatusUnprocessableEntity, "managed_postgres_unsupported", "Managed PostgreSQL feature unsupported"
	case errors.Is(err, managedpostgres.ErrNotInPlan):
		status, code, title = http.StatusForbidden, "managed_postgres_not_in_plan", "Managed PostgreSQL is not included in this plan"
	case errors.Is(err, managedpostgres.ErrQuotaExceeded):
		status, code, title = http.StatusForbidden, "managed_postgres_quota_exceeded", "Managed PostgreSQL quota exceeded"
	}
	if detail == "" {
		detail = "The managed PostgreSQL operation could not be completed."
	}
	return api.NewProblem(status, code, title, detail)
}
