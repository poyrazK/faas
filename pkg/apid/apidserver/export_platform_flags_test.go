package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/livequeries"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnablePlatformFlagForAll seeds a platform flag on for every account: the
// boot fallback FAAS_FLAGS_ENABLED=1 or FAAS_AI_GATEWAY_ENABLED=1 produces.
func (r *server) EnablePlatformFlagForAll(flagKey string) {
	r.seedPlatformFlag(flagKey, platformflags.FromAccountList(true, true, nil))
}

// SeedPlatformFlag installs a boot-time platform-flag definition.
func (r *server) SeedPlatformFlag(flagKey string, def platformflags.Definition) {
	r.seedPlatformFlag(flagKey, def)
}

// AdvancePlatformFlagRamps runs one pass of the automatic ramp worker.
func (r *server) AdvancePlatformFlagRamps(ctx context.Context, now time.Time) {
	r.advancePlatformFlagRamps(ctx, now)
}

// RealtimeEndpointPreviews decides the realtime preview flags for an endpoint.
func (r *server) RealtimeEndpointPreviews(ctx context.Context, accountID, appID string) (*realtime.EndpointPreviews, error) {
	return r.realtimeEndpointPreviews(ctx, accountID, appID)
}

// PlatformFlagRampActor is the audit actor of automatic ramp writes.
const PlatformFlagRampActor = platformFlagRampActor

// RegisterPlatformFlagRetireBy exports the preview retire-by gauge.
var RegisterPlatformFlagRetireBy = registerPlatformFlagRetireBy

// SeedEnvPlatformFlags seeds the env-gated flags' boot fallbacks from getenv.
func (r *server) SeedEnvPlatformFlags(getenv func(string) string) { r.seedEnvPlatformFlags(getenv) }

// ApplicationAuthEnabledFor is the application_auth release gate.
func (r *server) ApplicationAuthEnabledFor(subject platformflags.Subject) bool {
	return r.applicationAuthEnabled(subject)
}

// ChangeTimelineEnabledFor is the change_timeline release gate; legacy sets
// FAAS_CHANGE_TIMELINE_ENABLED's parsed value first.
func (r *server) ChangeTimelineEnabledFor(subject platformflags.Subject, legacy bool) bool {
	r.changeTimelineEnabled = legacy
	return r.changeTimelineFor(subject)
}

// AnomalyAlertsAllowedFor reports whether a baseline comparison is admitted
// for acct; legacy sets FAAS_ANOMALY_ALERTS_ENABLED's parsed value first.
func (r *server) AnomalyAlertsAllowedFor(subject platformflags.Subject, legacy bool) bool {
	r.anomalyAlertsEnabled = legacy
	return r.anomalyAlertsGate(subject, "above_baseline") == nil
}

// TriggerBatchDeliveryAllowedFor reports whether `delivery: batch` is
// admitted for acct; legacy sets the environment gate's parsed value first.
func (r *server) TriggerBatchDeliveryAllowedFor(subject platformflags.Subject, legacy bool) bool {
	r.triggerBatchDeliveryEnabled = legacy
	batch := api.TriggerDeliveryBatch
	_, problem := r.requestedTriggerDelivery(subject, api.TriggerKindKafka, &batch)
	return problem == nil
}

// FlagSubject is the subject a request decides platform flags for.
func FlagSubject(r *http.Request, acct state.Account) platformflags.Subject {
	return flagSubject(r, acct)
}

// OperationSchedulesOpen and OperationChildrenOpen are the gates apid wires
// into the operation service.
func (r *server) OperationSchedulesOpen(accountID string) bool {
	return r.operation.SchedulesReleased(accountID)
}

func (r *server) OperationChildrenOpen(accountID string) bool {
	return r.operation.ChildrenReleased(accountID)
}

// ServiceMapReleased is the service_map gate; legacy sets
// FAAS_SERVICE_MAP_ENABLED's parsed value first.
func (r *server) ServiceMapReleased(req *http.Request, acct state.Account, legacy bool) bool {
	r.serviceMap.Enabled = legacy
	return r.serviceMap.Released(req, acct)
}

// GuestTracingReleased is the guest_tracing gate; legacy sets
// FAAS_GUEST_TRACING_ENABLED's parsed value first.
func (r *server) GuestTracingReleased(acct state.Account, legacy bool) bool {
	r.guestTracingEnabled = legacy
	return r.guestTracingFor(acct)
}

// StaticEgressIPReleased is the static_egress_ip gate.
func (r *server) StaticEgressIPReleased(subject platformflags.Subject) bool {
	return r.staticEgressIPFor(subject)
}

// LiveGate is the live-query gate currently in effect.
func (r *server) LiveGate() livequeries.Gate { return r.liveGate() }

// PlatformFlagOn reports a platform flag's kill switch.
func (r *server) PlatformFlagOn(flagKey string) bool { return r.platformFlagOn(flagKey) }

// PlatformFlagSet exposes the compiled platform-flag holder.
func (r *server) PlatformFlagSet() *platformflags.Set { return r.platformFlags() }

// EnableRuntimeConfigReceipts makes the server's manager report apply
// receipts as daemon.
func (r *server) EnableRuntimeConfigReceipts(daemon string) {
	if r.runtimeConfig == nil {
		r.runtimeConfig = newRuntimeConfigManager(nil)
	}
	r.runtimeConfig.enableReceipts(daemon)
}

// ApplyRuntimeConfig installs a durable runtime-configuration value as if
// the subscriber had reconciled it at the given version.
func (r *server) ApplyRuntimeConfig(key string, value json.RawMessage, version int64) (bool, error) {
	if r.runtimeConfig == nil {
		r.runtimeConfig = newRuntimeConfigManager(nil)
	}
	return r.runtimeConfig.applyVersion(key, value, version)
}

// SetDatadogIntegrationEnabled sets FAAS_DATADOG_INTEGRATION_ENABLED's parsed
// value (ADR-20261024222102).
func (r *server) SetDatadogIntegrationEnabled(on bool) { r.datadogIntegrationEnabled = on }

// LogSearchReleasedForApp decides the log-search capability for runtime lines
// of app (ADR-20261024222102).
func (r *server) LogSearchReleasedForApp(ctx context.Context, app state.App) bool {
	return r.logSearchReleasedForApp(ctx, app)
}

// InstallCapabilityGate installs the production capability route gate
// (ADR-20261023115741) on a test server.
func (r *server) InstallCapabilityGate() error {
	gate, err := r.newCapabilityGate()
	if err != nil {
		return err
	}
	r.WithCapabilityGate(gate)
	return nil
}

// CheckPlatformFlagWrite runs the operator write validation for a platform
// flag value, as PATCH /v1/admin/config/platform_flag.<key> does.
func (r *server) CheckPlatformFlagWrite(flagKey string, value json.RawMessage, force bool) *api.Problem {
	if r.runtimeConfig == nil {
		r.runtimeConfig = newRuntimeConfigManager(nil)
	}
	def, ok := r.runtimeConfig.Definition(platformflags.ConfigKey(flagKey))
	if !ok {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Unknown configuration key", flagKey)
	}
	_, prob := r.checkRuntimeConfigChange(def, value, force)
	return prob
}

// CheckManifestSettings runs the gregale.yaml capability check that a source
// deploy applies before staging its workloads.
func (r *server) CheckManifestSettings(acct state.Account, manifest *gregalemanifest.Manifest) *api.Problem {
	return r.manifestSettingsProblem(acct, manifest)
}

// TemplateNames lists the catalog templates GET /v1/templates offers acct.
func (r *server) TemplateNames(acct state.Account) []string {
	rows := r.templateRows(platformflags.Subject{AccountID: acct.ID, Plan: string(acct.Plan)})
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return names
}

// EventsPausedProblem is the events-flag refusal the platform-tenant publish
// route applies (ADR-20261027125625).
func (r *server) EventsPausedProblem(acct state.Account, app state.App) *api.Problem {
	return r.eventsPausedProblem(acct, app)
}
