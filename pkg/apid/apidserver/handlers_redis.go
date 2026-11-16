package apidserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"

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
	mux.HandleFunc("POST /v1/redis/{name}/bindings", s.authLimited(s.requireMFA(s.requireScope(api.ScopesSecretsWriteSurface...)(s.requireVerifiedEmail(s.bindRedisCache)))))
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
	writeJSON(w, http.StatusOK, redisCacheResponse(app, cache))
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
	store, ok := s.redisStore(w)
	if !ok {
		return
	}
	var req api.CreateRedisCacheRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.MemoryMB == 0 {
		req.MemoryMB = min(api.ManagedValkeyDefaultMemoryMB, max(api.ManagedValkeyMaxMemoryFor(acct.Plan), api.ManagedValkeyMinMemoryMB))
	}
	if req.EvictionPolicy == "" {
		req.EvictionPolicy = api.ManagedValkeyDefaultEvictionPolicy
	}
	if req.IdleTimeoutS == 0 {
		req.IdleTimeoutS = api.ManagedValkeyDefaultIdleTimeoutS
	}
	limit := api.ManagedValkeyCachesFor(acct.Plan)
	if limit == 0 {
		api.WriteProblem(w, api.ErrManagedValkeyNotInPlan(acct.Plan))
		return
	}
	for _, problem := range []*api.Problem{
		api.ValidateManagedValkeyMemory(acct.Plan, req.MemoryMB),
		api.ValidateManagedValkeyEvictionPolicy(req.EvictionPolicy),
		api.ValidateManagedValkeyIdleTimeout(req.IdleTimeoutS),
	} {
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	existing, err := store.ListManagedValkeyCaches(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count Redis caches"))
		return
	}
	if len(existing) >= limit {
		api.WriteProblem(w, api.ErrManagedValkeyLimit(acct.Plan, limit))
		return
	}

	limits := api.MustLimitsFor(acct.Plan)
	app, problem := s.buildApp(acct, api.CreateAppRequest{
		Slug:       req.Name,
		Visibility: string(api.AppVisibilityInternal),
		RAMMB:      api.ManagedValkeyVMRAMMB(req.MemoryMB),
	}, limits)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	floor, ceiling := limits.IdleTimeoutBounds()
	app.IdleTimeoutS = min(max(req.IdleTimeoutS, floor), ceiling)
	app.MaxConcurrency = 1
	app.MinInstances = 0
	app.Manifest.Port = api.ManagedValkeyPort
	app.Manifest.Ports = []api.WorkloadPort{{Name: "redis", Port: api.ManagedValkeyPort, Protocol: api.WorkloadPortTCP, Internal: true}}
	app.NodeID = ""

	created, err := s.createAppIfUnderQuotaWithActivity(r.Context(), r, acct, app, limits)
	if err != nil {
		var qe *state.QuotaError
		switch {
		case errors.As(err, &qe):
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeValidation, "App limit reached",
				"a Redis cache counts as an app; delete an app or upgrade"))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Name taken",
				"the name "+req.Name+" is already in use by an app or cache"))
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create Redis cache"))
		}
		return
	}
	// One instance only: two would be two different caches.
	if _, err := s.store.UpdateApp(r.Context(), created.ID, state.UpdateAppParams{
		ScalingPolicy: &state.ScalingPolicy{MinInstances: 0, MaxInstances: 1}, SetScalingPolicy: true,
	}); err != nil {
		s.log.Warn("apid: redis cache scaling policy", "app_id", created.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not configure Redis cache"))
		return
	}
	cache, err := store.CreateManagedValkeyCache(r.Context(), state.ManagedValkeyCache{
		AppID: created.ID, AccountID: acct.ID, MemoryMB: req.MemoryMB, EvictionPolicy: req.EvictionPolicy,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not record Redis cache"))
		return
	}
	s.emitAppCreated(r.Context(), created)

	password, err := deriveRedisPassword(created.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate Redis password"))
		return
	}
	if problem := s.sealAndPersist(r.Context(), acct, created, api.DefaultEnvScope, api.ManagedValkeyPasswordEnv, password, api.SecretClassPersistent, limits); problem != nil {
		api.WriteProblem(w, problem)
		return
	}

	// Deploy through the ordinary image path so every deploy gate applies.
	rec := httptest.NewRecorder()
	s.createImageDeployment(rec, r, acct, created, limits, api.CreateDeploymentRequest{
		Image: api.ManagedValkeyImage,
		Overrides: &api.CreateDeploymentOverrides{
			Entrypoint: api.ManagedValkeyServerCommand(req.MemoryMB, req.EvictionPolicy),
			Port:       api.ManagedValkeyPort,
		},
	}, false)
	if rec.Code != http.StatusAccepted {
		s.log.Warn("apid: redis cache deploy refused", "app_id", created.ID, "status", rec.Code)
		for key, values := range rec.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	s.audit.Emit(r.Context(), "redis.created", &acct.ID, map[string]any{
		"app_id": created.ID, "name": created.Slug, "memory_mb": cache.MemoryMB, "eviction_policy": cache.EvictionPolicy,
	})
	writeJSON(w, http.StatusCreated, redisCacheResponse(created, cache))
}

func (s *server) deleteRedisCache(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, _, ok := s.loadRedisCache(w, r, acct)
	if !ok {
		return
	}
	// The ordinary app delete tears down the VM and its snapshots; the
	// marker row goes with the app (ON DELETE CASCADE at purge).
	r.SetPathValue("slug", app.Slug)
	s.deleteApp(w, r, acct)
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
	password, err := deriveRedisPassword(cacheApp.ID)
	if err != nil {
		s.log.Warn("apid: redis cache password", "app_id", cacheApp.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not read the Redis cache credential"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if problem := s.sealAndPersist(r.Context(), acct, target, api.DefaultEnvScope, req.EnvKey,
		api.ManagedValkeyURL(cacheApp.Slug, password), api.SecretClassPersistent, limits); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "redis.bound", &acct.ID, map[string]any{
		"cache_app_id": cacheApp.ID, "app_id": target.ID, "env_key": req.EnvKey,
	})
	writeJSON(w, http.StatusOK, api.RedisCacheBinding{Cache: cacheApp.Slug, App: target.Slug, EnvKey: req.EnvKey})
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

// redisPasswordLabel separates cache passwords from every other use of the
// host HMAC key.
const redisPasswordLabel = "gregale managed valkey password v1\x00"

// deriveRedisPassword is a cache's Valkey password: HMAC-SHA256 of its app id
// under apid's host HMAC key. apid cannot open sealed app secrets, so it
// derives the password again when a binding needs it instead of storing it.
// Slice 2 of ADR-20261116192126 moves AUTH into the service TCP proxy, after
// which apps never hold it.
func deriveRedisPassword(appID string) (string, error) {
	key := hostHMACKeyBytes()
	if len(key) == 0 {
		return "", errors.New("apid: host HMAC key unavailable")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(redisPasswordLabel + appID))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
