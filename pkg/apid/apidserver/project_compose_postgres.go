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
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
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
)

// composePostgresProvision is one Compose postgres service a project apply
// provides as a managed database.
type composePostgresProvision struct {
	service  string
	database string
	existing *managedpostgres.Database
	bindings []projectsetup.ManagedConsumer
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
	// Organization-owned projects bind databases through their own access
	// path; they keep attaching databases explicitly.
	if _, shared := projectaccess.From(ctx); shared {
		return notProvided(composePostgresUnavailable)
	}
	if _, shared := appaccess.FromContext(ctx); shared {
		return notProvided(composePostgresUnavailable)
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
		if !managedpostgres.ValidName(name) {
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
		provision := composePostgresProvision{service: m.Name, database: name, bindings: unset}
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
	ctx, err := s.composePostgresOwnership(ctx, acct)
	if err != nil {
		return api.ErrCapacity("could not resolve database ownership")
	}
	limits, ok := api.ManagedPostgresLimitsFor(api.Plan(acct.Plan))
	if !ok {
		return managedPostgresManifestProblem(managedpostgres.ErrNotInPlan, "managed PostgreSQL is not in this plan")
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
		spec, err := managedPostgresSpecFromRequest(api.CreateManagedPostgresDatabaseRequest{Region: s.managedPostgres.DefaultRegion()}, limits)
		if provision.existing != nil {
			spec, err = provision.existing.Spec, nil
		}
		if err != nil {
			return managedPostgresManifestProblem(err, fmt.Sprintf("Compose service %q: database configuration", service))
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
		Bindings: make([]api.PlanManagedBinding, 0, len(provision.bindings))}
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
