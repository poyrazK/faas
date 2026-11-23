package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Compose redis services become managed Redis caches (ADR-20261116192126,
// slice 3), as Compose postgres services become managed databases.

// Reasons a Compose redis service is not provided.
const (
	composeRedisUnavailable   = "managed Redis is not available to this account"
	composeRedisNoServiceTCP  = "managed Redis needs private TCP between services, which this installation has not enabled"
	composeRedisPlan          = "the plan includes no more managed Redis caches"
	composeRedisNotReferenced = "no selected service uses it"
	composeRedisConfigured    = api.ComposeRedisConfiguredReason
)

type composeRedisBinding struct {
	Workload string
	EnvKey   string
	Value    string
}

type composeRedisProvision struct {
	service  string
	cache    string
	existing bool
	bindings []composeRedisBinding
}

// composeRedisCacheName names a Compose service's cache. Cache names are app
// slugs: 3-40 lowercase letters, digits and hyphens, unique platform-wide.
func composeRedisCacheName(project, service, scope string) string {
	name := project + "-" + service
	if !state.IsProductionScope(scope) {
		name = project + "-" + scope + "-" + service
	}
	if len(name) <= 40 && state.IsProductionScope(scope) {
		return name
	}
	sum := sha256.Sum256([]byte(project + "\x00" + service + "\x00" + scope))
	if len(name) > 31 {
		name = strings.TrimRight(name[:31], "-")
	}
	return name + "-" + hex.EncodeToString(sum[:4])
}

// composeRedisValue rewrites a consumer's Compose value to point at the cache,
// keeping its shape: a URL stays a URL (database index kept, credentials
// dropped: caches have no password), host:port stays host:port, a bare host
// stays a bare host. A key with no value (from depends_on) gets the URL.
func composeRedisValue(original *string, cache string) string {
	if original == nil {
		return api.ManagedValkeyURL(cache)
	}
	value := strings.TrimSpace(*original)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return api.ManagedValkeyURL(cache)
		}
		parsed.Scheme = "redis"
		parsed.User = nil
		parsed.Host = api.ManagedValkeyAddr(cache)
		return parsed.String()
	}
	if _, _, err := net.SplitHostPort(value); err == nil {
		return api.ManagedValkeyAddr(cache)
	}
	return api.ManagedValkeyHost(cache)
}

// composeRedisPlan decides which Compose redis services apply provides as
// managed caches. Like postgres, a key that is already configured keeps its
// value, so an existing deployment is never rebound.
func (s *server) composeRedisPlan(ctx context.Context, acct state.Account, projectSlug, scope string,
	managed []reposcan.Managed, workloads []reposcan.Workload, env reposcan.ComposeEnv, apps []state.App,
) (map[string]composeRedisProvision, map[string]string, error) {
	provisions, reasons := map[string]composeRedisProvision{}, map[string]string{}
	var candidates []reposcan.Managed
	for _, m := range managed {
		if m.Kind == "redis" {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return provisions, reasons, nil
	}
	store, ok := s.store.(state.ManagedValkeyStore)
	if !ok || !s.capabilityEnabledFor(api.ManagedValkeyCapability, acct, state.App{}) {
		for _, m := range candidates {
			reasons[m.Name] = composeRedisUnavailable
		}
		return provisions, reasons, nil
	}
	serviceTCP := s.privateServiceTCPEnabled()
	existing, err := store.ListManagedValkeyCaches(ctx, acct.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("list managed caches: %w", err)
	}
	room := api.ManagedValkeyCachesFor(acct.Plan) - len(existing)
	byWorkload := make(map[string]state.App, len(apps))
	for _, app := range apps {
		if app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			byWorkload[app.WorkloadName] = app
		}
	}
	for _, m := range candidates {
		bindings, err := s.composeRedisBindings(ctx, acct, scope, env, workloads, m, byWorkload, composeRedisCacheName(projectSlug, m.Name, scope))
		if err != nil {
			return nil, nil, err
		}
		if bindings == nil {
			reasons[m.Name] = composeRedisNotReferenced
			continue
		}
		if len(bindings) == 0 {
			reasons[m.Name] = composeRedisConfigured
			continue
		}
		// Only a service that still needs a cache is held back by the
		// switch; one already bound reads as configured above.
		if !serviceTCP {
			reasons[m.Name] = composeRedisNoServiceTCP
			continue
		}
		provision := composeRedisProvision{service: m.Name, cache: composeRedisCacheName(projectSlug, m.Name, scope), bindings: bindings}
		provision.existing = s.ownsRedisCache(ctx, store, acct, provision.cache)
		if !provision.existing {
			if room <= 0 {
				reasons[m.Name] = composeRedisPlan
				continue
			}
			room--
		}
		provisions[m.Name] = provision
	}
	return provisions, reasons, nil
}

// composeRedisBindings lists the unset consumer keys of one redis service. It
// returns nil when no selected service uses it and an empty slice when every
// consumer already has its key set.
func (s *server) composeRedisBindings(ctx context.Context, acct state.Account, scope string, env reposcan.ComposeEnv,
	workloads []reposcan.Workload, m reposcan.Managed, byWorkload map[string]state.App, cache string,
) ([]composeRedisBinding, error) {
	consumers := projectsetup.ManagedConsumers(env, workloads, m.Name, m.EnvHint)
	if len(consumers) == 0 {
		return nil, nil
	}
	bindings := []composeRedisBinding{}
	for _, consumer := range consumers {
		if app, exists := byWorkload[consumer.Workload]; exists {
			keys, err := projectsetup.UnsetEnvKeys(ctx, s.store, acct.ID, scope, &app, []string{consumer.EnvKey})
			if err != nil {
				return nil, fmt.Errorf("read %s configuration: %w", consumer.Workload, err)
			}
			if len(keys) == 0 {
				continue
			}
		}
		bindings = append(bindings, composeRedisBinding{
			Workload: consumer.Workload, EnvKey: consumer.EnvKey,
			Value: composeRedisValue(env.Services[consumer.Workload][consumer.EnvKey], cache),
		})
	}
	return bindings, nil
}

// ownsRedisCache reports whether name is already one of the account's caches.
func (s *server) ownsRedisCache(ctx context.Context, store state.ManagedValkeyStore, acct state.Account, name string) bool {
	app, err := s.store.AppBySlug(ctx, name)
	if err != nil || app.AccountID != acct.ID || app.Status == state.AppDeleted {
		return false
	}
	_, err = store.GetManagedValkeyCache(ctx, app.ID)
	return err == nil
}

// applyComposeRedis creates each planned cache (or reuses the account's cache
// with its name) and writes each consumer's key after the project's apps
// exist.
func (s *server) applyComposeRedis(r *http.Request, acct state.Account, scope string,
	provisions map[string]composeRedisProvision, apps []state.App,
) *api.Problem {
	if len(provisions) == 0 {
		return nil
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
	limits := api.MustLimitsFor(acct.Plan)
	for _, service := range services {
		provision := provisions[service]
		if !provision.existing {
			if _, _, problem := s.provisionRedisCache(r, acct, api.CreateRedisCacheRequest{Name: provision.cache}); problem != nil {
				return problemWithDetail(problem, fmt.Sprintf("Compose service %q: create cache %q", service, provision.cache))
			}
		}
		for _, binding := range provision.bindings {
			app, ok := byWorkload[binding.Workload]
			if !ok {
				return api.NewProblem(http.StatusConflict, "managed_redis_binding_target_missing", "Cache binding target missing",
					fmt.Sprintf("Compose service %q: no app for workload %q", service, binding.Workload))
			}
			if problem := s.sealAndPersist(r.Context(), acct, app, scope, binding.EnvKey, binding.Value, api.SecretClassPersistent, limits); problem != nil {
				return problem
			}
		}
		s.audit.Emit(r.Context(), "redis.compose_provisioned", &acct.ID, map[string]any{
			"cache": provision.cache, "service": service, "bindings": len(provision.bindings), "existing": provision.existing,
		})
	}
	return nil
}

// problemWithDetail prefixes a problem's detail with where it happened.
func problemWithDetail(problem *api.Problem, where string) *api.Problem {
	if problem == nil {
		return nil
	}
	out := *problem
	out.Detail = where + ": " + out.Detail
	return &out
}

// withoutRedisProvidedKeys drops keys a provided cache will set.
func withoutRedisProvidedKeys(workload string, unset []string, provisions map[string]composeRedisProvision) []string {
	if len(provisions) == 0 {
		return unset
	}
	return slices.DeleteFunc(slices.Clone(unset), func(key string) bool {
		for _, provision := range provisions {
			if slices.ContainsFunc(provision.bindings, func(b composeRedisBinding) bool { return b.Workload == workload && b.EnvKey == key }) {
				return true
			}
		}
		return false
	})
}

// planRedisProvision is the plan view of a cache provision.
func planRedisProvision(provision composeRedisProvision) *api.PlanManagedProvision {
	out := &api.PlanManagedProvision{Database: provision.cache, Existing: provision.existing,
		Bindings: make([]api.PlanManagedBinding, 0, len(provision.bindings))}
	for _, binding := range provision.bindings {
		out.Bindings = append(out.Bindings, api.PlanManagedBinding{Workload: binding.Workload, EnvKey: binding.EnvKey})
	}
	return out
}
