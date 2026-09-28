package main

import (
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type appConfigActivityChange struct {
	Field          string `json:"field"`
	Old            any    `json:"old,omitempty"`
	New            any    `json:"new,omitempty"`
	ValuesRedacted bool   `json:"values_redacted,omitempty"`
}

// appConfigActivityChanges only projects explicitly selected fields from the
// existing before/after audit snapshot. Scalar operational settings are
// useful to explain a change; complex policy values are named but withheld so
// CIDRs, service policy details, and manifest contents never leak into the
// shared workspace timeline.
func appConfigActivityChanges(oldValues, newValues map[string]any) []appConfigActivityChange {
	fields := []string{
		"resource_profile", "ram_mb", "cpu_millicores", "max_concurrency",
		"request_rate_limit_rps", "request_rate_limit_burst", "idle_timeout_s", "min_instances",
		"autoscale_target_rps", "autoscale_target_cpu_pct", "streaming_enabled", "websocket_enabled",
		"warm_snapshot_enabled", "warm_snapshot_min_requests", "warm_snapshot_min_ms", "warm_pool_size",
		"eviction_priority", "require_authn", "consumer_auth_mode", "public_auth", "visibility",
		"egress_allowlist", "allowed_service_callers", "allowed_service_call_scopes",
		"service_bindings", "service_binding_policy", "service_binding_transport", "lifecycle",
	}
	withValues := map[string]struct{}{
		"resource_profile": {}, "ram_mb": {}, "cpu_millicores": {}, "max_concurrency": {},
		"request_rate_limit_rps": {}, "request_rate_limit_burst": {}, "idle_timeout_s": {}, "min_instances": {},
		"autoscale_target_rps": {}, "autoscale_target_cpu_pct": {}, "streaming_enabled": {}, "websocket_enabled": {},
		"warm_snapshot_enabled": {}, "warm_snapshot_min_requests": {}, "warm_snapshot_min_ms": {}, "warm_pool_size": {},
		"eviction_priority": {}, "require_authn": {}, "consumer_auth_mode": {}, "public_auth": {}, "visibility": {},
	}

	changes := make([]appConfigActivityChange, 0, len(fields))
	for _, field := range fields {
		oldValue, oldOK := oldValues[field]
		newValue, newOK := newValues[field]
		if !oldOK || !newOK || reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		change := appConfigActivityChange{Field: field}
		if _, ok := withValues[field]; ok {
			change.Old = oldValue
			change.New = newValue
		} else {
			change.ValuesRedacted = true
		}
		changes = append(changes, change)
	}
	return changes
}

// appConfigActivityChangesForUpdate builds the safe workspace projection from
// the exact rows passed by the store's atomic mutation callback. It mirrors
// the API audit's touched-field selection without carrying the full (and in
// some cases secret-bearing) manifest into the activity event.
func appConfigActivityChangesForUpdate(req api.UpdateAppRequest, before, after state.App, plan api.Plan, callerPolicySet, callScopesSet, lifecycleChanged bool) []appConfigActivityChange {
	oldValues := make(map[string]any)
	newValues := make(map[string]any)
	add := func(touched bool, field string, oldValue, newValue any) {
		if touched && !reflect.DeepEqual(oldValue, newValue) {
			oldValues[field] = oldValue
			newValues[field] = newValue
		}
	}
	add(req.ResourceProfile != nil, "resource_profile",
		api.ResourceProfileForResources(before.RAMMB, effectiveAppCPUMillicores(before, plan)),
		api.ResourceProfileForResources(after.RAMMB, effectiveAppCPUMillicores(after, plan)))
	add(req.RAMMB != nil, "ram_mb", before.RAMMB, after.RAMMB)
	add(req.CPUMillicores != nil, "cpu_millicores", effectiveAppCPUMillicores(before, plan), effectiveAppCPUMillicores(after, plan))
	add(req.MaxConcurrency != nil, "max_concurrency", before.MaxConcurrency, after.MaxConcurrency)
	beforeRPS, beforeBurst := appRequestRateLimits(before, plan)
	afterRPS, afterBurst := appRequestRateLimits(after, plan)
	add(req.RequestRateLimitRPS != nil, "request_rate_limit_rps", beforeRPS, afterRPS)
	add(req.RequestRateLimitBurst != nil, "request_rate_limit_burst", beforeBurst, afterBurst)
	add(req.IdleTimeoutS != nil, "idle_timeout_s", before.IdleTimeoutS, after.IdleTimeoutS)
	add(req.MinInstances != nil, "min_instances", before.MinInstances, after.MinInstances)
	add(req.AutoscaleTargetRPS != nil, "autoscale_target_rps", before.AutoscaleTargetRPS, after.AutoscaleTargetRPS)
	add(req.AutoscaleTargetCPUPct != nil, "autoscale_target_cpu_pct", before.AutoscaleTargetCPUPct, after.AutoscaleTargetCPUPct)
	add(req.StreamingEnabled != nil, "streaming_enabled", before.StreamingEnabled, after.StreamingEnabled)
	add(req.WebSocketEnabled != nil, "websocket_enabled", before.WebSocketEnabled, after.WebSocketEnabled)
	add(req.WarmSnapshotEnabled != nil, "warm_snapshot_enabled", before.WarmSnapshotEnabled, after.WarmSnapshotEnabled)
	add(req.WarmSnapshotMinRequests != nil, "warm_snapshot_min_requests", before.WarmSnapshotMinRequests, after.WarmSnapshotMinRequests)
	add(req.WarmSnapshotMinMs != nil, "warm_snapshot_min_ms", before.WarmSnapshotMinMs, after.WarmSnapshotMinMs)
	add(req.WarmPoolSize != nil, "warm_pool_size", before.WarmPoolSize, after.WarmPoolSize)
	add(req.EvictionPriority != nil, "eviction_priority", before.EvictionPriority, after.EvictionPriority)
	add(req.RequireAuthn != nil, "require_authn", before.RequireAuthn, after.RequireAuthn)
	add(req.ConsumerAuthMode != nil, "consumer_auth_mode", string(before.ConsumerAuthMode), string(after.ConsumerAuthMode))
	add(req.PublicAuth != nil, "public_auth", before.PublicAuthMode, after.PublicAuthMode)
	add(req.Visibility != nil, "visibility", string(api.NormalizeAppVisibility(before.Visibility)), string(api.NormalizeAppVisibility(after.Visibility)))
	add(req.EgressAllowlist != nil, "egress_allowlist", egressStringList(before.EgressAllowlist), egressStringList(after.EgressAllowlist))
	add(callerPolicySet, "allowed_service_callers", before.Manifest.AllowedServiceCallers, after.Manifest.AllowedServiceCallers)
	add(callScopesSet, "allowed_service_call_scopes", before.Manifest.AllowedServiceCallScopes, after.Manifest.AllowedServiceCallScopes)
	add(req.ServiceBindingTargets != nil, "service_bindings", before.Manifest.ServiceBindings, after.Manifest.ServiceBindings)
	add(req.ServiceBindingPolicy != nil, "service_binding_policy", before.Manifest.EffectiveServiceBindingPolicy(), after.Manifest.EffectiveServiceBindingPolicy())
	add(req.ServiceBindingTransport != nil, "service_binding_transport", before.Manifest.EffectiveServiceBindingTransport(), after.Manifest.EffectiveServiceBindingTransport())
	add(lifecycleChanged, "lifecycle", apiManifestFromState(before.Manifest), apiManifestFromState(after.Manifest))
	return appConfigActivityChanges(oldValues, newValues)
}
