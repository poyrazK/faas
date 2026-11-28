package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Managed Redis-compatible caches on Valkey (ADR-20261116192126).
//
// A cache is an internal app that runs api.ManagedValkeyImage on port 6379
// with at most one instance. The app gives it placement, scale to zero,
// billing and deletion; managed_valkey_caches marks it and holds its
// settings. Apps reach it at redis://<name>.svc.gregale:6379 through the
// private TCP service proxy.

func (s *server) registerRedisRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/redis", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.listRedisCaches))))
	mux.HandleFunc("POST /v1/redis", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.createRedisCache))))))
	mux.HandleFunc("GET /v1/redis/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.getRedisCache))))
	mux.HandleFunc("DELETE /v1/redis/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.deleteRedisCache))))
	mux.HandleFunc("PATCH /v1/redis/{name}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.updateRedisCache))))))
	mux.HandleFunc("POST /v1/redis/{name}/upgrade", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.requireVerifiedEmail(s.idempotent(s.upgradeRedisCache))))))
	mux.HandleFunc("POST /v1/redis/{name}/restart", s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.idempotent(s.restartRedisCache)))))
	mux.HandleFunc("POST /v1/redis/{name}/bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesSecretsWriteSurface...)(s.requireVerifiedEmail(s.bindRedisCache)))))
	mux.HandleFunc("DELETE /v1/redis/{name}/bindings/{app}", s.authLimited(s.requireMFA(s.requireScope(api.ScopesSecretsWriteSurface...)(s.unbindRedisCache))))
}

func (s *server) redisStore(w http.ResponseWriter) (state.ManagedValkeyStore, bool) {
	store, ok := s.store.(state.ManagedValkeyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed Redis is unavailable on this installation"))
		return nil, false
	}
	return store, true
}

func (s *server) listRedisCaches(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.redisStore(w)
	if !ok {
		return
	}
	caches, err := store.ListManagedValkeyCaches(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list Redis caches"))
		return
	}
	out := api.ListRedisCachesResponse{Caches: []api.RedisCache{}, Limit: api.ManagedValkeyCachesFor(acct.Plan)}
	for _, cache := range caches {
		app, err := s.store.AppByID(r.Context(), cache.AppID)
		if err != nil {
			continue
		}
		out.Caches = append(out.Caches, redisCacheResponse(app, cache))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, cache, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	response := redisCacheResponse(app, cache)
	// `gregale redis cli` runs through a bound app; it picks the only one.
	if grants, ok := s.store.(state.ManagedValkeyGrantStore); ok {
		bound, err := grants.ListManagedValkeyCacheGrantees(r.Context(), app.ID)
		if err != nil {
			s.log.WarnContext(r.Context(), "apid: list managed cache bindings", "cache_app_id", app.ID, "err", err)
		}
		response.BoundApps = bound
	}
	writeJSON(w, http.StatusOK, response)
}

// loadRedisCache resolves {name} to the caller's cache app.
func (s *server) loadRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ManagedValkeyCache, bool) {
	store, ok := s.redisStore(w)
	if !ok {
		return state.App{}, state.ManagedValkeyCache{}, false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("name"))
	if !ok {
		return state.App{}, state.ManagedValkeyCache{}, false
	}
	cache, err := store.GetManagedValkeyCache(r.Context(), app.ID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found",
			"no Redis cache named "+app.Slug+"; it is an app, not a cache"))
		return state.App{}, state.ManagedValkeyCache{}, false
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not load Redis cache"))
		return state.App{}, state.ManagedValkeyCache{}, false
	}
	return app, cache, true
}

func (s *server) createRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateRedisCacheRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, cache, problem := s.provisionRedisCache(r, acct, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusCreated, redisCacheResponse(app, cache))
}

// redisCacheDefaults fills a create request's omitted settings.
func redisCacheDefaults(plan api.Plan, req api.CreateRedisCacheRequest) api.CreateRedisCacheRequest {
	if req.MemoryMB == 0 {
		req.MemoryMB = min(api.ManagedValkeyDefaultMemoryMB, max(api.ManagedValkeyMaxMemoryFor(plan), api.ManagedValkeyMinMemoryMB))
	}
	if req.EvictionPolicy == "" {
		req.EvictionPolicy = api.ManagedValkeyDefaultEvictionPolicy
	}
	if req.IdleTimeoutS == 0 {
		req.IdleTimeoutS = api.ManagedValkeyDefaultIdleTimeoutS
	}
	return req
}

// redisCacheAdmission checks a create request against the plan.
func (s *server) redisCacheAdmission(r *http.Request, store state.ManagedValkeyStore, acct state.Account, req api.CreateRedisCacheRequest) *api.Problem {
	limit := api.ManagedValkeyCachesFor(acct.Plan)
	if limit == 0 {
		return api.ErrManagedValkeyNotInPlan(acct.Plan)
	}
	for _, problem := range []*api.Problem{
		api.ValidateManagedValkeyMemory(acct.Plan, req.MemoryMB),
		api.ValidateManagedValkeyEvictionPolicy(req.EvictionPolicy),
		api.ValidateManagedValkeyIdleTimeout(req.IdleTimeoutS),
	} {
		if problem != nil {
			return problem
		}
	}
	existing, err := store.ListManagedValkeyCaches(r.Context(), acct.ID)
	if err != nil {
		return api.ErrCapacity("could not count Redis caches")
	}
	if len(existing) >= limit {
		return api.ErrManagedValkeyLimit(acct.Plan, limit)
	}
	return nil
}

// provisionRedisCache creates a cache: its internal app, the marker row and
// the Valkey deployment. The HTTP handler and Compose provisioning share it.
func (s *server) provisionRedisCache(r *http.Request, acct state.Account, req api.CreateRedisCacheRequest) (state.App, state.ManagedValkeyCache, *api.Problem) {
	store, ok := s.store.(state.ManagedValkeyStore)
	if !ok {
		return state.App{}, state.ManagedValkeyCache{}, api.ErrCapacity("managed Redis is unavailable on this installation")
	}
	if !s.privateServiceTCPEnabled() {
		return state.App{}, state.ManagedValkeyCache{}, errPrivateServiceTCPOff()
	}
	req = redisCacheDefaults(acct.Plan, req)
	if problem := s.redisCacheAdmission(r, store, acct, req); problem != nil {
		return state.App{}, state.ManagedValkeyCache{}, problem
	}
	created, problem := s.createRedisCacheApp(r, acct, req)
	if problem != nil {
		return state.App{}, state.ManagedValkeyCache{}, problem
	}
	cache, err := store.CreateManagedValkeyCache(r.Context(), state.ManagedValkeyCache{
		AppID: created.ID, AccountID: acct.ID, MemoryMB: req.MemoryMB, EvictionPolicy: req.EvictionPolicy,
	})
	if err != nil {
		return state.App{}, state.ManagedValkeyCache{}, api.ErrCapacity("could not record Redis cache")
	}
	s.emitAppCreated(r.Context(), created)
	if problem := s.deployRedisCache(r, acct, created, req); problem != nil {
		return state.App{}, state.ManagedValkeyCache{}, problem
	}
	s.audit.Emit(r.Context(), "redis.created", &acct.ID, map[string]any{
		"app_id": created.ID, "name": created.Slug, "memory_mb": cache.MemoryMB, "eviction_policy": cache.EvictionPolicy,
	})
	return created, cache, nil
}

// createRedisCacheApp creates the cache's internal app: port 6379, at most
// one instance (two would be two different caches), scale to zero.
func (s *server) createRedisCacheApp(r *http.Request, acct state.Account, req api.CreateRedisCacheRequest) (state.App, *api.Problem) {
	limits := api.MustLimitsFor(acct.Plan)
	app, problem := s.buildApp(acct, api.CreateAppRequest{
		Slug:       req.Name,
		Visibility: string(api.AppVisibilityInternal),
		RAMMB:      api.ManagedValkeyVMRAMMB(req.MemoryMB),
	}, limits)
	if problem != nil {
		return state.App{}, problem
	}
	floor, ceiling := limits.IdleTimeoutBounds()
	app.IdleTimeoutS = min(max(req.IdleTimeoutS, floor), ceiling)
	app.MaxConcurrency = 1
	app.MinInstances = 0
	app.Manifest.Port = api.ManagedValkeyPort
	// The handoff opens port 6379 only after replication catches up, so
	// a resize or upgrade gets the plan's whole startup window.
	app.Manifest.StartupDeadlineS = limits.MaxStartupDeadlineS
	app.Manifest.Ports = []api.WorkloadPort{{Name: "redis", Port: api.ManagedValkeyPort, Protocol: api.WorkloadPortTCP, Internal: true}}
	app.NodeID = ""
	created, err := s.createAppIfUnderQuotaWithActivity(r.Context(), r, acct, app, limits)
	var qe *state.QuotaError
	switch {
	case errors.As(err, &qe):
		return state.App{}, api.NewProblem(http.StatusForbidden, api.CodeValidation, "App limit reached",
			"a Redis cache counts as an app; delete an app or upgrade")
	case errors.Is(err, state.ErrConflict):
		return state.App{}, api.NewProblem(http.StatusConflict, api.CodeValidation, "Name taken",
			"the name "+req.Name+" is already in use by an app or cache")
	case err != nil:
		return state.App{}, api.ErrCapacity("could not create Redis cache")
	}
	if _, err := s.store.UpdateApp(r.Context(), created.ID, state.UpdateAppParams{
		ScalingPolicy: &state.ScalingPolicy{MinInstances: 0, MaxInstances: 1}, SetScalingPolicy: true,
	}); err != nil {
		s.log.Warn("apid: redis cache scaling policy", "app_id", created.ID, "err", err)
		return state.App{}, api.ErrCapacity("could not configure Redis cache")
	}
	return created, nil
}

// deployRedisCache deploys the pinned Valkey image through the ordinary image
// path, so every deploy gate applies.
func (s *server) deployRedisCache(r *http.Request, acct state.Account, app state.App, req api.CreateRedisCacheRequest) *api.Problem {
	rec := httptest.NewRecorder()
	s.createImageDeployment(rec, r, acct, app, api.MustLimitsFor(acct.Plan), api.CreateDeploymentRequest{
		Image: api.ManagedValkeyImage,
		Overrides: &api.CreateDeploymentOverrides{
			Entrypoint: api.ManagedValkeyServerCommand(req.MemoryMB, req.EvictionPolicy),
			Port:       api.ManagedValkeyPort,
		},
	}, false)
	if rec.Code == http.StatusAccepted {
		return nil
	}
	s.log.Warn("apid: redis cache deploy refused", "app_id", app.ID, "status", rec.Code)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code == "" {
		return api.ErrCapacity("could not deploy Redis cache")
	}
	problem.Status = rec.Code
	return &problem
}

func (s *server) deleteRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, _, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	if problem := s.redisCacheStillBound(r.Context(), app); problem != nil {
		s.refuseRedisChange(w, app, problem, nil)
		return
	}
	// The ordinary app delete tears down the VM and its snapshots; the
	// marker row goes with the app (ON DELETE CASCADE at purge).
	r.SetPathValue("slug", app.Slug)
	s.deleteApp(w, r, acct)
}

// redisCacheStillBound refuses to delete a cache live apps are bound to.
// Deleting it left each app's connection variable pointing at a cache that
// answers every command "ERR cache unavailable, retry the command" (prod
// hunt r50); managed Postgres refuses the same way while apps are attached.
func (s *server) redisCacheStillBound(ctx context.Context, cacheApp state.App) *api.Problem {
	grants, ok := s.store.(state.ManagedValkeyGrantStore)
	if !ok {
		return nil
	}
	bound, err := grants.ListManagedValkeyCacheGrantees(ctx, cacheApp.ID)
	if err != nil {
		s.log.WarnContext(ctx, "apid: list managed cache bindings", "cache_app_id", cacheApp.ID, "err", err)
		return api.ErrCapacity("could not check the cache's bindings; retry the delete")
	}
	if len(bound) == 0 {
		return nil
	}
	steps := make([]string, 0, len(bound))
	for _, app := range bound {
		steps = append(steps, fmt.Sprintf("gregale redis unbind %s --app %s", cacheApp.Slug, app))
	}
	return api.NewProblem(http.StatusConflict, "redis_cache_bound", "Cache is still bound",
		fmt.Sprintf("%s is bound to %s; deleting it would leave their connection variable pointing at a deleted cache",
			cacheApp.Slug, strings.Join(bound, ", "))).
		WithHint("Unbind each app (" + strings.Join(steps, "; ") + "), unset its connection variable with gregale env unset, then delete the cache.")
}

// updateRedisCache resizes a cache or changes its eviction policy through the
// replication handoff (ADR-20261116192126 slice 4): the new VM copies the
// running cache before it serves, so no data is lost.
func (s *server) updateRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, cache, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	var req api.UpdateRedisCacheRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	next := cache
	if req.MemoryMB != 0 {
		next.MemoryMB = req.MemoryMB
	}
	if req.EvictionPolicy != "" {
		next.EvictionPolicy = req.EvictionPolicy
	}
	if next.MemoryMB == cache.MemoryMB && next.EvictionPolicy == cache.EvictionPolicy {
		api.WriteProblem(w, api.ErrValidation("nothing to change: give memory_mb or eviction_policy"))
		return
	}
	if problem := firstProblem(api.ValidateManagedValkeyMemory(acct.Plan, next.MemoryMB), api.ValidateManagedValkeyEvictionPolicy(next.EvictionPolicy)); problem != nil {
		s.refuseRedisChange(w, app, problem, nil)
		return
	}
	if next.MemoryMB < cache.MemoryMB && !req.Evict {
		s.refuseRedisChange(w, app, errRedisShrinkNeedsEvict(app.Slug, cache, next), nil)
		return
	}
	s.handOffRedisCache(w, r, acct, app, next)
}

// errRedisShrinkNeedsEvict refuses a shrink the caller did not allow to evict
// keys, and names the flag that allows it.
func errRedisShrinkNeedsEvict(name string, cache, next state.ManagedValkeyCache) *api.Problem {
	command := fmt.Sprintf("gregale redis resize %s --memory %d", name, next.MemoryMB)
	if next.EvictionPolicy != cache.EvictionPolicy {
		command += " --eviction-policy " + next.EvictionPolicy
	}
	return api.NewProblem(http.StatusConflict, "cache_shrink_needs_evict", "Shrinking needs --evict",
		fmt.Sprintf("%s holds up to %d MB and would shrink to %d MB, which may not fit its data; set evict to let the cache drop keys by its eviction policy", name, cache.MemoryMB, next.MemoryMB)).
		WithHint("Run " + command + " --evict.")
}

// refuseRedisChange writes a refused cache change and logs it, so a refusal a
// customer reports can be found by the cache's app ID.
// cause is the underlying error behind a server-side refusal, or nil; a 5xx
// refusal logs at Warn so it surfaces next to other failures.
func (s *server) refuseRedisChange(w http.ResponseWriter, app state.App, problem *api.Problem, cause error) {
	attrs := []any{"app_id", app.ID, "status", problem.Status, "code", problem.Code, "detail", problem.Detail}
	if cause != nil {
		attrs = append(attrs, "err", cause)
	}
	if problem.Status >= http.StatusInternalServerError {
		s.log.Warn("apid: redis cache change refused", attrs...)
	} else {
		s.log.Info("apid: redis cache change refused", attrs...)
	}
	api.WriteProblem(w, problem)
}

// upgradeRedisCache moves a cache to the current Valkey image through the
// same handoff, keeping its size, policy and data.
func (s *server) upgradeRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, cache, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	s.handOffRedisCache(w, r, acct, app, cache)
}

// handOffRedisCache records the cache's next settings and deploys a VM that
// copies the running cache before it serves.
func (s *server) handOffRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, next state.ManagedValkeyCache) {
	store, ok := s.redisStore(w)
	if !ok {
		return
	}
	previous, err := store.GetManagedValkeyCache(r.Context(), app.ID)
	if err != nil {
		s.refuseRedisChange(w, app, api.ErrCapacity("could not load Redis cache"), err)
		return
	}
	previousRAM := app.RAMMB
	rollback := func(ctx context.Context) {
		if _, err := s.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &previousRAM}); err != nil {
			s.log.Warn("apid: redis cache change rollback", "app_id", app.ID, "err", err)
		}
		if _, err := store.UpdateManagedValkeyCache(ctx, app.ID, previous.MemoryMB, previous.EvictionPolicy); err != nil {
			s.log.Warn("apid: redis cache change rollback", "app_id", app.ID, "err", err)
		}
	}
	// The handoff copies from the running VM. A parked cache's snapshot has
	// the old RAM shape and cannot restore once the app's RAM changes, so it
	// would cold-boot empty and the copy would be empty (native run
	// redis-20261118T095202). Wake it first, while the RAM still matches.
	if problem := s.redisEnsureRunning(r.Context(), acct, app); problem != nil {
		s.refuseRedisChange(w, app, problem, nil)
		return
	}
	ram := api.ManagedValkeyVMRAMMB(next.MemoryMB)
	if ram != app.RAMMB {
		updated, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{RAMMB: &ram})
		if err != nil {
			s.refuseRedisChange(w, app, api.ErrCapacity("could not resize Redis cache"), err)
			return
		}
		app = updated
	}
	cache, err := store.UpdateManagedValkeyCache(r.Context(), app.ID, next.MemoryMB, next.EvictionPolicy)
	if err != nil {
		rollback(r.Context())
		s.refuseRedisChange(w, app, api.ErrCapacity("could not record Redis cache change"), err)
		return
	}
	until := timeNow().Unix() + api.ManagedValkeyHandoffWindowS
	rec := httptest.NewRecorder()
	s.createImageDeployment(rec, r, acct, app, api.MustLimitsFor(acct.Plan), api.CreateDeploymentRequest{
		Image: api.ManagedValkeyImage,
		Overrides: &api.CreateDeploymentOverrides{
			Entrypoint: api.ManagedValkeyHandoffCommand(s.redisHandoffSource(r.Context(), app), cache.MemoryMB, cache.EvictionPolicy, until),
			Port:       api.ManagedValkeyPort,
		},
	}, false)
	if rec.Code != http.StatusAccepted {
		// The running VM keeps serving with its old settings.
		rollback(r.Context())
		var problem api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code == "" {
			s.refuseRedisChange(w, app, api.ErrCapacity("could not deploy the Redis cache change"), err)
			return
		}
		problem.Status = rec.Code
		s.refuseRedisChange(w, app, &problem, nil)
		return
	}
	var dep api.DeploymentResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &dep)
	s.audit.Emit(r.Context(), "redis.handoff_started", &acct.ID, map[string]any{
		"app_id": app.ID, "name": app.Slug, "memory_mb": cache.MemoryMB, "eviction_policy": cache.EvictionPolicy, "deployment_id": dep.ID,
	})
	writeJSON(w, http.StatusAccepted, api.RedisCacheChange{Cache: redisCacheResponse(app, cache), DeploymentID: dep.ID})
}

func firstProblem(problems ...*api.Problem) *api.Problem {
	for _, problem := range problems {
		if problem != nil {
			return problem
		}
	}
	return nil
}

// restartRedisCache restarts a cache's VM and keeps its data: schedd's normal
// restart snapshots the running instance and wakes from that snapshot. A
// fresh restart, which discards snapshots, is never used for a cache.
func (s *server) restartRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, _, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	if app.Status != state.AppActive {
		s.refuseRedisChange(w, app, redisRestartRefusal(app), nil)
		return
	}
	query := r.URL.Query()
	query.Del("fresh")
	r.URL.RawQuery = query.Encode()
	r.SetPathValue("slug", app.Slug)
	s.restartApp(w, r, acct)
}

// redisRestartRefusal explains a restart of a cache that is not running. A
// parked cache wakes with its data on its next connection; one between VMs
// during a resize or update shows as parked too until the new VM serves.
// Neither names the backing app, which customers never see.
func redisRestartRefusal(app state.App) *api.Problem {
	if app.Status == state.AppEvictedCold {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Redis cache is not running",
			fmt.Sprintf("%s is parked or switching to a new VM after a resize or update, so nothing is running to restart; its next connection starts it with its data", app.Slug)).
			WithHint(fmt.Sprintf("No restart is needed. Check its state with gregale redis get %s.", app.Slug))
	}
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Redis cache is not active",
		fmt.Sprintf("%s is %s; only an active cache can be restarted", app.Slug, app.Status))
}

func (s *server) bindRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	cacheApp, _, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	var req api.BindRedisCacheRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.EnvKey == "" {
		req.EnvKey = api.ManagedValkeyDefaultBindingEnv
	}
	if req.App == "" {
		api.WriteProblem(w, api.ErrValidation("app is required"))
		return
	}
	if p := api.ValidateEnvKey(req.EnvKey); p != nil {
		api.WriteProblem(w, p)
		return
	}
	target, ok := s.loadApp(w, r, acct, req.App)
	if !ok {
		return
	}
	if target.ID == cacheApp.ID {
		api.WriteProblem(w, api.ErrValidation("a cache cannot be bound to itself"))
		return
	}
	if problem := s.grantRedisCache(r.Context(), acct, cacheApp, target); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if problem := s.sealAndPersist(r.Context(), acct, target, api.DefaultEnvScope, req.EnvKey,
		api.ManagedValkeyURL(cacheApp.Slug), api.SecretClassPersistent, limits); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "redis.bound", &acct.ID, map[string]any{
		"cache_app_id": cacheApp.ID, "app_id": target.ID, "env_key": req.EnvKey,
	})
	writeJSON(w, http.StatusOK, api.RedisCacheBinding{Cache: cacheApp.Slug, App: target.Slug, EnvKey: req.EnvKey})
}

// grantRedisCache lets app call the cache: binding grants the call
// (ADR-20261124174500), so a project workload reaches a cache outside its
// project under the default project policy.
func (s *server) grantRedisCache(ctx context.Context, acct state.Account, cacheApp, app state.App) *api.Problem {
	grants, ok := s.store.(state.ManagedValkeyGrantStore)
	if !ok {
		return api.ErrCapacity("managed Redis is unavailable on this installation")
	}
	if err := grants.GrantManagedValkeyCache(ctx, cacheApp.ID, app.ID, acct.ID); err != nil {
		s.log.ErrorContext(ctx, "grant managed cache", "cache_app_id", cacheApp.ID, "app_id", app.ID, "err", err)
		return api.ErrCapacity("could not record the cache binding")
	}
	return nil
}

// unbindRedisCache revokes an app's grant to call the cache. The app keeps
// its connection variable until it is unset; calls are refused from now on.
func (s *server) unbindRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	cacheApp, _, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	grants, ok := s.store.(state.ManagedValkeyGrantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("managed Redis is unavailable on this installation"))
		return
	}
	target, ok := s.loadApp(w, r, acct, r.PathValue("app"))
	if !ok {
		return
	}
	revoked, err := grants.RevokeManagedValkeyCacheGrant(r.Context(), cacheApp.ID, target.ID)
	if err != nil {
		s.log.ErrorContext(r.Context(), "revoke managed cache grant", "cache_app_id", cacheApp.ID, "app_id", target.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not remove the cache binding"))
		return
	}
	if !revoked {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found",
			fmt.Sprintf("%s is not bound to Redis cache %s", target.Slug, cacheApp.Slug)))
		return
	}
	s.audit.Emit(r.Context(), "redis.unbound", &acct.ID, map[string]any{"cache_app_id": cacheApp.ID, "app_id": target.ID})
	writeJSON(w, http.StatusOK, api.RedisCacheUnbinding{Cache: cacheApp.Slug, App: target.Slug})
}

func redisCacheResponse(app state.App, cache state.ManagedValkeyCache) api.RedisCache {
	return api.RedisCache{
		Name:           app.Slug,
		AppID:          app.ID,
		MemoryMB:       cache.MemoryMB,
		EvictionPolicy: cache.EvictionPolicy,
		IdleTimeoutS:   app.IdleTimeoutS,
		Engine:         "valkey",
		Host:           api.ManagedValkeyHost(app.Slug),
		Port:           api.ManagedValkeyPort,
		Status:         string(app.Status),
		CreatedAt:      cache.CreatedAt,
	}
}

// redisHandoffSource is the address a handoff replicates from: the cache's
// private service IP, so the copy does not depend on guest DNS, or its
// service name when the address cannot be read.
func (s *server) redisHandoffSource(ctx context.Context, app state.App) string {
	index, err := s.store.AppServiceAddressIndex(ctx, app.ID)
	if err == nil {
		if addr, ok := api.ServiceAddressForIndex(index); ok {
			return addr.String()
		}
	}
	s.log.Warn("apid: redis handoff falls back to the service name", "app_id", app.ID, "err", err)
	return api.ManagedValkeyHost(app.Slug)
}

// redisWakeWait bounds how long a resize or upgrade waits for a parked cache
// to wake before it hands off. Tests shorten it.
var redisWakeWait = 60 * time.Second

// redisEnsureRunning wakes the cache and waits until it has a running
// instance. A cache that does not wake in time is left unchanged.
func (s *server) redisEnsureRunning(ctx context.Context, acct state.Account, app state.App) *api.Problem {
	if _, err := s.store.RunningInstanceForApp(ctx, app.ID); err == nil {
		return nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return api.ErrCapacity("could not read the Redis cache's running instance")
	}
	dep, problem := s.explicitWakeDeployment(ctx, app)
	if problem != nil {
		return problem
	}
	if _, err := s.enqueueExplicitAppWakeFor(ctx, acct, app, s.frozenInstanceToResume(ctx, app.ID, dep.ID)); err != nil {
		return api.ErrCapacity("could not wake the Redis cache")
	}
	deadline := time.Now().Add(redisWakeWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return api.ErrCapacity("request ended while the Redis cache was waking")
		case <-time.After(250 * time.Millisecond):
		}
		if _, err := s.store.RunningInstanceForApp(ctx, app.ID); err == nil {
			return nil
		}
	}
	return api.NewProblem(http.StatusServiceUnavailable, "cache_waking", "Redis cache is waking",
		"the cache did not wake in time; it is unchanged, retry the command")
}

// privateServiceTCPEnabled reports whether the data plane serves private
// service TCP (ADR-576), the only way an app reaches a managed cache. The
// switch defaults on and is a kill switch for when a node loses the listener;
// a server without runtime configuration counts it as on.
func (s *server) privateServiceTCPEnabled() bool {
	return s.runtimeBool(runtimeConfigPrivateServiceTCP, true)
}

// errPrivateServiceTCPOff refuses a cache that no app could connect to.
func errPrivateServiceTCPOff() *api.Problem {
	return api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented, "Managed Redis unavailable",
		"managed Redis needs private TCP between services, which is not enabled on this Gregale installation right now; contact support, or set REDIS_URL to a Redis you run elsewhere")
}
