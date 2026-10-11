package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
	entitysocket "github.com/onebox-faas/faas/pkg/durableentity/socket"
	"github.com/onebox-faas/faas/pkg/state"
)

func durableEntityTenantLimits(getenv func(string) string, entityBytes int64) (entitysocket.TenantLimits, error) {
	limits := entitysocket.TenantLimits{Connections: 8, GrantsPerMinute: 60, InvocationsPerMinute: 120, RetainedBytes: 16 << 20, EntityBytes: entityBytes}
	for _, entry := range []struct {
		name    string
		value   *int
		maximum int
	}{
		{"FAAS_DURABLE_ENTITY_TENANT_MAX_CONNECTIONS", &limits.Connections, 32},
		{"FAAS_DURABLE_ENTITY_TENANT_GRANTS_PER_MINUTE", &limits.GrantsPerMinute, 256},
		{"FAAS_DURABLE_ENTITY_TENANT_INVOCATIONS_PER_MINUTE", &limits.InvocationsPerMinute, 10000},
	} {
		if raw := getenv(entry.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > entry.maximum || strconv.Itoa(value) != raw {
				return limits, errors.New("invalid " + entry.name)
			}
			*entry.value = value
		}
	}
	if raw := getenv("FAAS_DURABLE_ENTITY_TENANT_MAX_RETAINED_BYTES"); raw != "" {
		value, err := durableEntityStorageLimit(raw)
		if err != nil {
			return limits, err
		}
		limits.RetainedBytes = value
	}
	if entityBytes <= 0 || entityBytes > 64<<20 || limits.RetainedBytes < entityBytes || limits.RetainedBytes > 1<<30 {
		return limits, errors.New("tenant quotas require a positive per-entity retained-byte cap up to 64 MiB and a tenant cap between that cap and 1 GiB")
	}
	return limits, nil
}
func writeEntityTenantQuotaProblem(w http.ResponseWriter, err error) {
	if errors.Is(err, durableentity.ErrRetired) {
		api.WriteProblem(w, api.NewProblem(410, "durable_entity_retired", "Entity retired", "this entity identity is permanently retired; use a new identity for new work"))
		return
	}
	if errors.Is(err, entitysocket.ErrRegistryCleanupPending) {
		api.WriteProblem(w, api.NewProblem(503, "durable_entity_registry_cleanup_pending", "Registry cleanup pending", "this operation is blocked until registry cleanup completes"))
		return
	}
	if errors.Is(err, entitysocket.ErrPolicyMismatch) {
		api.WriteProblem(w, api.NewProblem(503, "durable_entity_quota_policy_mismatch", "Quota policy mismatch", "this node's configured limits differ from the approved scope policy"))
		return
	}
	if errors.Is(err, entitysocket.ErrMigrationPending) {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "durable_entity_quota_migration_pending", "Quota migration pending", "scope admission is held until reservation backfill and verification complete"))
		return
	}
	var quota *entitysocket.QuotaError
	if errors.As(err, &quota) {
		if quota.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(quota.RetryAfter))
		}
		problem := api.NewProblem(http.StatusTooManyRequests, "durable_entity_tenant_quota", "Tenant quota reached", quota.Budget+" admission budget reached").WithLimit(quota.Limit, quota.Observed).WithDocs("https://gregale.dev/docs")
		retry := int64(quota.RetryAfter)
		if retry > 0 {
			problem.RetryAfterSeconds = &retry
		}
		api.WriteProblem(w, problem)
		return
	}
	if errors.Is(err, entitysocket.ErrGrantDenied) {
		grantProblem(w, err)
		return
	}
	if errors.Is(err, durableentity.ErrInvalid) {
		api.WriteProblem(w, api.ErrValidation("invalid tenant quota scope"))
		return
	}
	api.WriteProblem(w, api.ErrCapacity("tenant quota admission unavailable; retry without changing the request identity"))
}

// Connection reservations live longer than the transport's bounded lifetime
// and in-flight invocation. A crashed node cannot immediately free its slot.
func (s *server) reserveEntityTenantConnection(w http.ResponseWriter, r *http.Request, id durableentity.ID) (string, func(), bool) {
	if s.durableEntityTenantQuotas == nil {
		return "", func() {}, true
	}
	quotas := s.durableEntityTenantQuotas
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	lease, err := quotas.AcquireConnection(ctx, id)
	if err != nil {
		writeEntityTenantQuotaProblem(w, err)
		return "", nil, false
	}
	return lease, func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		_ = quotas.ReleaseConnection(ctx, id, lease)
	}, true
}
func (s *server) inspectEntityTenantQuotas(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.durableEntityTenantQuotas == nil {
		api.WriteProblem(w, api.ErrCapacity("tenant quota preview is disabled"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !s.durableEntityApps[app.ID] {
		api.WriteProblem(w, api.ErrCapacity("app is not enabled for durable entities"))
		return
	}
	selectors, err := durableEntityInspectionQuery(r.URL.RawQuery)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("provide exact entity selectors"))
		return
	}
	id, _, problem := s.resolveDurableEntityIdentity(r, acct, app, api.DurableEntityInvokeRequest{Namespace: selectors.Namespace, Key: selectors.Key, Environment: selectors.Environment, PlatformTenantID: selectors.PlatformTenantID}, true)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	usage, err := s.durableEntityTenantQuotas.Inspect(ctx, id)
	if err != nil {
		writeEntityTenantQuotaProblem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, usage)
}
