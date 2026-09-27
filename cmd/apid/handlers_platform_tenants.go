package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) platformTenantStore(w http.ResponseWriter, acct state.Account) (state.PlatformTenantStore, bool) {
	if !s.consumerFeatureAllowed(w, acct) {
		return nil, false
	}
	store, ok := s.store.(state.PlatformTenantStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenants are unavailable"))
	}
	return store, ok
}

func platformTenantResponse(tenant state.PlatformTenant) api.PlatformTenantResponse {
	return api.PlatformTenantResponse{ID: tenant.ID, ExternalRef: tenant.ExternalRef, Name: tenant.Name,
		Status: tenant.Status, CreatedAt: tenant.CreatedAt, UpdatedAt: tenant.UpdatedAt}
}

func (s *server) platformTenantByPath(w http.ResponseWriter, r *http.Request, acct state.Account, store state.PlatformTenantStore) (state.PlatformTenant, bool) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		s.notFound(w, "no such platform tenant")
		return state.PlatformTenant{}, false
	}
	tenant, err := store.GetPlatformTenant(r.Context(), acct.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant")
		return state.PlatformTenant{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant"))
		return state.PlatformTenant{}, false
	}
	return tenant, true
}

func (s *server) createPlatformTenant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	var req api.CreatePlatformTenantRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req.ExternalRef, req.Name = strings.TrimSpace(req.ExternalRef), strings.TrimSpace(req.Name)
	if req.ExternalRef == "" || len(req.ExternalRef) > 256 || req.Name == "" || len(req.Name) > 128 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid platform tenant", "external_ref must be 1-256 characters and name must be 1-128 characters"))
		return
	}
	tenant, created, err := store.CreatePlatformTenant(r.Context(), acct.ID, req.ExternalRef, req.Name, acct.Plan.ConsumerKeysPerAccount())
	var quota *state.PlatformTenantQuotaError
	if errors.As(err, &quota) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_quota",
			"Platform tenant quota reached", "this account has reached its plan's platform tenant limit"))
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Platform tenant already exists", "external_ref is already registered with another name"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create platform tenant"))
		return
	}
	if created {
		s.audit.Emit(r.Context(), "platform_tenant.created", &acct.ID, map[string]any{
			"tenant_id": tenant.ID, "external_ref": tenant.ExternalRef,
		})
		writeJSON(w, http.StatusCreated, platformTenantResponse(tenant))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantResponse(tenant))
}

func (s *server) listPlatformTenants(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	limit, offset := 100, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be an integer from 1 to 100"))
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid offset", "offset must be a non-negative integer"))
			return
		}
		offset = parsed
	}
	tenants, err := store.ListPlatformTenants(r.Context(), acct.ID, limit, offset)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenants"))
		return
	}
	out := api.PlatformTenantListResponse{Tenants: make([]api.PlatformTenantResponse, 0, len(tenants))}
	for _, tenant := range tenants {
		out.Tenants = append(out.Tenants, platformTenantResponse(tenant))
	}
	if len(tenants) == limit {
		next := offset + len(tenants)
		out.NextOffset = &next
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getPlatformTenant(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, store)
	if !ok {
		return
	}
	consumers, err := store.ListPlatformTenantConsumers(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant consumers"))
		return
	}
	surfaces, err := store.ListPlatformTenantSurfaces(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant surfaces"))
		return
	}
	out := api.PlatformTenantDetailResponse{PlatformTenantResponse: platformTenantResponse(tenant),
		Consumers: make([]api.APIConsumerResponse, 0, len(consumers)),
		Surfaces:  make([]api.PlatformTenantSurfaceResponse, 0, len(surfaces))}
	for _, consumer := range consumers {
		out.Consumers = append(out.Consumers, consumerResponse(consumer))
	}
	for _, surface := range surfaces {
		out.Surfaces = append(out.Surfaces, api.PlatformTenantSurfaceResponse{
			ID: surface.ID, AppID: surface.AppID, Name: surface.Name, Status: string(surface.Status),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) setPlatformTenantStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, store)
	if !ok {
		return
	}
	var req api.SetPlatformTenantStatusRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Status != state.PlatformTenantActive && req.Status != state.PlatformTenantSuspended {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid platform tenant status", "status must be active or suspended"))
		return
	}
	updated, err := store.SetPlatformTenantStatus(r.Context(), acct.ID, tenant.ID, req.Status)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not update platform tenant"))
		return
	}
	if tenant.Status != updated.Status {
		s.audit.Emit(r.Context(), "platform_tenant.status_changed", &acct.ID, map[string]any{
			"tenant_id": tenant.ID, "old_status": tenant.Status, "status": updated.Status,
		})
	}
	writeJSON(w, http.StatusOK, platformTenantResponse(updated))
}

func (s *server) linkPlatformTenantConsumer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, store)
	if !ok {
		return
	}
	var req api.LinkPlatformTenantConsumerRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if _, err := uuid.Parse(req.ConsumerID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid consumer", "consumer_id must be a UUID"))
		return
	}
	consumer, err := store.LinkPlatformTenantConsumer(r.Context(), acct.ID, tenant.ID, req.ConsumerID)
	if !s.platformTenantLinkError(w, err) {
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.consumer_linked", &acct.ID, map[string]any{
		"tenant_id": tenant.ID, "consumer_id": consumer.ID, "app_id": consumer.AppID,
	})
	writeJSON(w, http.StatusOK, consumerResponse(consumer))
}

func (s *server) linkPlatformTenantSurface(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, store)
	if !ok {
		return
	}
	var req api.LinkPlatformTenantSurfaceRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if _, err := uuid.Parse(req.SurfaceID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid surface", "surface_id must be a UUID"))
		return
	}
	surface, err := store.LinkPlatformTenantSurface(r.Context(), acct.ID, tenant.ID, req.SurfaceID)
	if !s.platformTenantLinkError(w, err) {
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.surface_linked", &acct.ID, map[string]any{
		"tenant_id": tenant.ID, "surface_id": surface.ID, "app_id": surface.AppID,
	})
	writeJSON(w, http.StatusOK, api.PlatformTenantSurfaceResponse{
		ID: surface.ID, AppID: surface.AppID, Name: surface.Name, Status: string(surface.Status),
	})
}

func (s *server) platformTenantLinkError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such platform tenant resource")
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Resource already linked", "the resource belongs to another platform tenant"))
	default:
		api.WriteProblem(w, api.ErrInternal("could not link platform tenant resource"))
	}
	return false
}

func (s *server) getPlatformTenantUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, store)
	if !ok {
		return
	}
	since, until, problem := parseUsageWindow(r, time.Now().UTC())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	rows, err := store.ListPlatformTenantUsage(r.Context(), acct.ID, tenant.ID, since, until)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant usage"))
		return
	}
	out := api.PlatformTenantUsageResponse{TenantID: tenant.ID, PeriodStart: since, PeriodEnd: until,
		Buckets: make([]api.PlatformTenantUsageBucketResponse, 0, len(rows)), AsOf: time.Now().UTC()}
	for _, row := range rows {
		out.RequestCount += row.RequestCount
		out.ErrorCount += row.ErrorCount
		out.BillableUnits += row.BillableUnits
		out.Buckets = append(out.Buckets, api.PlatformTenantUsageBucketResponse{
			AppID: row.AppID, ConsumerID: row.ConsumerKey, SurfaceID: row.SurfaceID, JWTAuthorizationRuleID: row.JWTAuthorizationRuleID, WindowStart: row.WindowStart,
			RequestCount: row.RequestCount, ErrorCount: row.ErrorCount, BillableUnits: row.BillableUnits,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
