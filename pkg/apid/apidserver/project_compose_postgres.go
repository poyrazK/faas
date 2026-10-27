package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Reasons a Compose postgres service is not provided (ADR-20261025111824).
const (
	composePostgresUnavailable   = "unavailable"
	composePostgresPlan          = "plan"
	composePostgresQuota         = "quota"
	composePostgresUnsupported   = "unsupported"
	composePostgresConfigured    = "configured"
	composePostgresNotReferenced = "not_referenced"
	// composePostgresPermission: in an organization-owned project, the
	// caller's role or key cannot create and bind organization databases
	// (ADR-20261026151530).
	composePostgresPermission = "permission"
)

// sharedComposePostgres reports whether ctx acts on an organization-owned
// project or app, where creating and binding a database are organization
// actions.
func sharedComposePostgres(ctx context.Context) bool {
	_, sharedProject := projectaccess.From(ctx)
	_, sharedApp := appaccess.FromContext(ctx)
	return sharedProject || sharedApp
}

// authorizeComposePostgres applies the organization permissions and key
// scope that `gregale postgres create` and a manifest database binding need.
// The plan asks with no audit emitter, so a dry run records no denial; apply
// asks with one.
func authorizeComposePostgres(ctx context.Context, audit authz.AuditEmitter) *api.Problem {
	if problem := authz.AuthorizeOrgAction(ctx, authz.OrgActionManagePostgres, audit); problem != nil {
		return problem
	}
	return postgresaccess.AuthorizeManifestBindings(ctx, audit)
}

// composePostgresProvision is one Compose postgres service a project apply
// provides as a managed database.
type composePostgresProvision struct {
	service  string
	database string
	spec     managedpostgres.Spec // what apply creates; an existing database keeps its own
	existing *managedpostgres.Database
	bindings []projectsetup.ManagedConsumer
}

// composePostgresMajor picks the managed PostgreSQL major for a Compose
// postgres image: the image's own major when offered, else the nearest newer
// one, because a newer server keeps the app's clients and SQL working and an
// older one may not. An image without a numeric tag (`postgres`,
// `postgres:latest`) takes the newest offered major, as it does locally.
func composePostgresMajor(image string, offered []int) (int, bool) {
	want := imageTagMajor(image)
	best := 0
	for _, major := range offered {
		switch {
		case want == 0 && major > best:
			best = major
		case want != 0 && major >= want && (best == 0 || major < best):
			best = major
		}
	}
	return best, best != 0
}

// imageTagMajor reads the leading major version of an image tag:
// postgres:17, postgres:16.4-alpine, bitnami/postgresql:15.6.0 and
// pgvector/pgvector:pg16 all name one; an untagged or named tag returns 0.
func imageTagMajor(image string) int {
	image, _, _ = strings.Cut(image, "@")
	colon := strings.LastIndexByte(image, ':')
	if colon < 0 || colon < strings.LastIndexByte(image, '/') {
		return 0
	}
	tag := strings.TrimPrefix(image[colon+1:], "pg")
	major := 0
	for _, ch := range tag {
		if ch < '0' || ch > '9' || major > 1000 {
			break
		}
		major = major*10 + int(ch-'0')
	}
	return major
}

// composePostgresPlan decides, for each Compose datastore the plan lists,
// whether apply provides it as a managed PostgreSQL database. It provides a
// postgres service only where managed PostgreSQL is open to the account and
// the plan allows a development database, and only for the selected
// services whose key is still unset: a key already configured keeps its
// database, so an existing deployment is never rebound.
func (s *server) composePostgresPlan(ctx context.Context, acct state.Account, projectSlug, scope string,
	managed []reposcan.Managed, workloads []reposcan.Workload, env reposcan.ComposeEnv, apps []state.App,
) (map[string]composePostgresProvision, map[string]string, error) {
	provisions, reasons := map[string]composePostgresProvision{}, map[string]string{}
	var candidates []reposcan.Managed
	for _, m := range managed {
		switch m.Kind {
		case "postgres":
			candidates = append(candidates, m)
		case "postgis":
			reasons[m.Name] = composePostgresUnsupported
		}
	}
	if len(candidates) == 0 {
		return provisions, reasons, nil
	}
	notProvided := func(reason string) (map[string]composePostgresProvision, map[string]string, error) {
		for _, m := range candidates {
			reasons[m.Name] = reason
		}
		return provisions, reasons, nil
	}
	if s.managedPostgres == nil || s.managedPostgresBindings == nil {
		return notProvided(composePostgresUnavailable)
	}
	// An organization-owned project provides an organization database, where
	// the caller may create and bind one (ADR-20261026151530).
	if sharedComposePostgres(ctx) && authorizeComposePostgres(ctx, nil) != nil {
		return notProvided(composePostgresPermission)
	}
	ctx, err := s.composePostgresOwnership(ctx, acct)
	if err != nil {
		return nil, nil, err
	}
	discovery, err := s.managedPostgres.DiscoverCapabilities(ctx, acct.ID, "")
	if err != nil || !discovery.ProvisioningEnabled {
		return notProvided(composePostgresUnavailable)
	}
	limits, ok := api.ManagedPostgresLimitsFor(api.Plan(acct.Plan))
	if !ok || limits.DatabasesMax == 0 || !limits.DevelopmentAllowed {
		return notProvided(composePostgresPlan)
	}
	databases, err := s.managedPostgres.List(ctx, acct.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("list managed databases: %w", err)
	}
	room := min(limits.DatabasesMax, discovery.DatabaseLimit) - len(databases)
	byWorkload := make(map[string]state.App, len(apps))
	for _, app := range apps {
		if app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			byWorkload[app.WorkloadName] = app
		}
	}
	for _, m := range candidates {
		name := projectSlug + "-" + m.Name
		major, offered := composePostgresMajor(m.Image, discovery.Capabilities.PostgresMajors)
		if !managedpostgres.ValidName(name) || !offered {
			reasons[m.Name] = composePostgresUnsupported
			continue
		}
		spec, err := managedPostgresSpecFromRequest(api.CreateManagedPostgresDatabaseRequest{Region: discovery.Region, PostgresMajor: major}, limits)
		if errors.Is(err, managedpostgres.ErrNotInPlan) || errors.Is(err, managedpostgres.ErrQuotaExceeded) {
			reasons[m.Name] = composePostgresPlan
			continue
		}
		if err != nil || discovery.Capabilities.Supports(spec) != nil {
			reasons[m.Name] = composePostgresUnsupported
			continue
		}
		consumers := projectsetup.ManagedConsumers(env, workloads, m.Name, m.EnvHint)
		if len(consumers) == 0 {
			reasons[m.Name] = composePostgresNotReferenced
			continue
		}
		var unset []projectsetup.ManagedConsumer
		for _, consumer := range consumers {
			app, exists := byWorkload[consumer.Workload]
			if !exists {
				unset = append(unset, consumer)
				continue
			}
			keys, err := projectsetup.UnsetEnvKeys(ctx, s.store, acct.ID, scope, &app, []string{consumer.EnvKey})
			if err != nil {
				return nil, nil, fmt.Errorf("read %s configuration: %w", consumer.Workload, err)
			}
			if len(keys) == 1 {
				unset = append(unset, consumer)
			}
		}
		if len(unset) == 0 {
			reasons[m.Name] = composePostgresConfigured
			continue
		}
		provision := composePostgresProvision{service: m.Name, database: name, spec: spec, bindings: unset}
		if i := slices.IndexFunc(databases, func(d managedpostgres.Database) bool { return d.Name == name }); i >= 0 {
			existing := databases[i]
			provision.existing = &existing
		} else if room <= 0 {
			reasons[m.Name] = composePostgresQuota
			continue
		} else {
			room--
		}
		provisions[m.Name] = provision
	}
	return provisions, reasons, nil
}

// composePostgresOwnership scopes managed PostgreSQL calls to the personal
// account's ownership, as the manifest binding path does.
func (s *server) composePostgresOwnership(ctx context.Context, acct state.Account) (context.Context, error) {
	if managedpostgres.Organization(ctx) != "" {
		return ctx, nil
	}
	org, err := s.store.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return ctx, fmt.Errorf("resolve database ownership: %w", err)
	}
	return managedpostgres.WithOwnership(ctx, org.ID, acct.ID), nil
}

// applyComposePostgres creates each planned database (or reuses the one with
// its name) and reserves its bindings after the project's apps exist. A
// database that is still provisioning gets its bindings reserved anyway; the
// binding reconciler injects each key as soon as the database is ready.
func (s *server) applyComposePostgres(ctx context.Context, acct state.Account, scope string,
	provisions map[string]composePostgresProvision, apps []state.App,
) *api.Problem {
	if len(provisions) == 0 {
		return nil
	}
	if sharedComposePostgres(ctx) {
		if problem := authorizeComposePostgres(ctx, loadOrgAuditFrom(s.audit)); problem != nil {
			return problem
		}
	}
	ctx, err := s.composePostgresOwnership(ctx, acct)
	if err != nil {
		return api.ErrCapacity("could not resolve database ownership")
	}
	byWorkload := make(map[string]state.App, len(apps))
	for _, app := range apps {
		if app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			byWorkload[app.WorkloadName] = app
		}
	}
	services := make([]string, 0, len(provisions))
	for service := range provisions {
		services = append(services, service)
	}
	slices.Sort(services)
	for _, service := range services {
		provision := provisions[service]
		spec := provision.spec
		if provision.existing != nil {
			spec = provision.existing.Spec
		}
		database, err := s.managedPostgres.Create(ctx, managedpostgres.CreateRequest{AccountID: acct.ID, Name: provision.database, Spec: spec})
		if err != nil {
			return managedPostgresManifestProblem(err, fmt.Sprintf("Compose service %q: create database %q", service, provision.database))
		}
		for _, binding := range provision.bindings {
			app, ok := byWorkload[binding.Workload]
			if !ok {
				return api.NewProblem(http.StatusConflict, "managed_postgres_binding_target_missing", "Database binding target missing",
					fmt.Sprintf("Compose service %q: no app for workload %q", service, binding.Workload))
			}
			if _, _, err := s.managedPostgresBindings.CreateWithResult(ctx, managedpostgres.CreateBindingRequest{
				AccountID: acct.ID, DatabaseAccountID: database.AccountID, DatabaseID: database.ID, AppID: app.ID,
				Scope: scope, EnvironmentKey: binding.EnvKey, Access: managedpostgres.CredentialReadWrite,
			}); err != nil {
				return managedPostgresManifestProblem(err, fmt.Sprintf("Compose service %q: bind %s to %s", service, binding.EnvKey, binding.Workload))
			}
		}
		s.audit.Emit(ctx, "managed_postgres.compose_provisioned", &acct.ID, map[string]any{
			"database_id": database.ID, "database": database.Name, "service": service, "bindings": len(provision.bindings),
		})
	}
	return nil
}

// planManagedProvision is the plan view of a provision.
func planManagedProvision(provision composePostgresProvision) *api.PlanManagedProvision {
	out := &api.PlanManagedProvision{Database: provision.database, Existing: provision.existing != nil,
		PostgresMajor: provision.spec.PostgresMajor, Bindings: make([]api.PlanManagedBinding, 0, len(provision.bindings))}
	if provision.existing != nil {
		out.PostgresMajor = provision.existing.Spec.PostgresMajor
	}
	for _, binding := range provision.bindings {
		out.Bindings = append(out.Bindings, api.PlanManagedBinding{Workload: binding.Workload, EnvKey: binding.EnvKey})
	}
	return out
}

// withoutProvidedKeys drops keys a provided database will bind from a
// workload's unset list.
func withoutProvidedKeys(workload string, unset []string, provisions map[string]composePostgresProvision) []string {
	if len(provisions) == 0 {
		return unset
	}
	bound := map[string]bool{}
	for _, provision := range provisions {
		for _, binding := range provision.bindings {
			if binding.Workload == workload {
				bound[strings.ToUpper(binding.EnvKey)] = true
			}
		}
	}
	var out []string
	for _, key := range unset {
		if !bound[strings.ToUpper(key)] {
			out = append(out, key)
		}
	}
	return out
}
