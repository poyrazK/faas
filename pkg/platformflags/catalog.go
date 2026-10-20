// Package platformflags is Gregale's operator-managed release gating for
// platform features (ADR-1700): which accounts may use a preview, a canary or
// a staged rollout, changed at runtime without a deploy.
//
// A platform flag is a runtime-configuration row (runtime_config_entries)
// whose JSON value is a Definition. apid validates and writes it; every daemon
// that gates on it keeps a compiled Snapshot current through the shared
// runtime-config watcher. Platform flags never grant a plan entitlement: a
// caller checks the flag AND the plan (pkg/productcap), so turning a flag on
// for a Free account does not unlock a paid capability.
//
// They are not customer Flags (pkg/flags, ADR-377), which release a customer's
// own application behavior to that customer's end users. Platform flags only
// reuse its stable rollout hash.
package platformflags

import (
	"slices"
	"strings"
)

const (
	// configKeyPrefix namespaces platform-flag rows inside the shared
	// runtime-configuration table and catalog.
	configKeyPrefix = "platform_flag."
	// GroupsConfigKey is the runtime-configuration row holding the shared
	// account groups.
	GroupsConfigKey = "platform_flag_groups"
)

// Flag keys. Each one is consumed through Set.Enabled by the daemons listed
// in its Spec.Consumers.
const (
	AIGateway            = "ai_gateway"
	CustomerFlags        = "customer_flags"
	ApplicationAuth      = "application_auth"
	LiveQueries          = "live_queries"
	ChangeTimeline       = "change_timeline"
	AnomalyAlerts        = "anomaly_alerts"
	TriggerBatchDelivery = "trigger_batch_delivery"
	APIMonetization      = "api_monetization"
)

// Spec declares one platform flag. Adding a flag is a code change: the
// catalog is the contract between the operator surface and the daemons that
// evaluate it.
type Spec struct {
	Key         string
	Label       string
	Description string
	// Capability is the pkg/productcap capability the flag releases, if any.
	Capability string
	// ADR is the decision that introduced the gate.
	ADR string
	// Consumers are the daemons that evaluate the flag.
	Consumers []string
	// RetireBy is the date (YYYY-MM-DD) by which a preview flag should be
	// removed or folded into general availability; empty for a kill switch
	// with no planned end.
	RetireBy string
	// LegacyEnv names the environment variables read as the boot fallback
	// before an operator has written a value.
	LegacyEnv []string
	// Default applies when neither the environment nor an operator value is
	// present.
	Default Definition
}

var catalog = []Spec{
	{
		Key:         AIGateway,
		Label:       "AI gateway",
		Description: "Release the AI gateway preview to accounts. The kill switch turns the gateway off platform-wide; plan entitlement is still enforced.",
		Capability:  "ai-gateway",
		ADR:         "ADR-998",
		Consumers:   []string{"apid", "outboundd"},
		LegacyEnv:   []string{"FAAS_AI_GATEWAY_ENABLED", "FAAS_AI_GATEWAY_ACCOUNTS"},
	},
	{
		Key:         CustomerFlags,
		Label:       "Customer Flags",
		Description: "Release customer feature flags (configuration, runtime reads and rollout automation) to accounts.",
		Capability:  "feature-flags",
		ADR:         "ADR-377",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_FLAGS_ENABLED"},
	},
	{
		Key:         ApplicationAuth,
		Label:       "Application Auth",
		Description: "Release application Auth (the Auth screens and the auth credential mode) to accounts. Apps already using Auth keep running while it is off.",
		ADR:         "ADR-1190",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_APPLICATION_AUTH_ENABLED", "FAAS_APPLICATION_AUTH_ACCOUNTS"},
	},
	{
		Key:         LiveQueries,
		Label:       "Live queries",
		Description: "Release live queries (incremental SQL results over realtime) to accounts. Decided by account only: plan rules never match. Unless every account is included, the live-query routes stay hidden from accounts outside it.",
		ADR:         "ADR-20261016151947",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_LIVE_QUERIES_ACCOUNTS"},
	},
	{
		Key:         ChangeTimeline,
		Label:       "Change timeline",
		Description: "Release the app change timeline preview (GET /v1/apps/{slug}/changes and its dashboard page) to accounts.",
		ADR:         "ADR-751",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_CHANGE_TIMELINE_ENABLED"},
	},
	{
		Key:         AnomalyAlerts,
		Label:       "Anomaly alerts",
		Description: "Release baseline alert comparisons (above_baseline, below_baseline) to accounts. Existing rules keep evaluating while it is off; only creating or changing one is refused.",
		ADR:         "ADR-744",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_ANOMALY_ALERTS_ENABLED"},
	},
	{
		Key:         TriggerBatchDelivery,
		Label:       "Trigger batch delivery",
		Description: "Release batch delivery (delivery: batch) on triggers to accounts. Triggers that already deliver in batches keep doing so while it is off; only creating one or switching one to batch is refused.",
		ADR:         "ADR-1321",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_TRIGGER_BATCH_DELIVERY_PREVIEW_ENABLED"},
	},
	{
		Key:   APIMonetization,
		Label: "API monetization",
		Description: "Release API monetization (rate cards, plans, statements, credits, invoice exports, billing runs and their gateway enforcement) " +
			"to accounts. The api_monetization_enabled runtime switch still releases it to every account. Unless every account is included, " +
			"its routes answer an unregistered route to accounts outside the flag after authentication.",
		ADR:       "ADR-20261020170649",
		Consumers: []string{"apid", "gatewayd-internal"},
	},
}

// Catalog returns every declared platform flag in a stable order.
func Catalog() []Spec {
	out := make([]Spec, len(catalog))
	for i, spec := range catalog {
		spec.Consumers = slices.Clone(spec.Consumers)
		spec.LegacyEnv = slices.Clone(spec.LegacyEnv)
		spec.Default = NormalizeDefinition(spec.Default)
		out[i] = spec
	}
	return out
}

// Lookup finds a flag by key.
func Lookup(key string) (Spec, bool) {
	for _, spec := range Catalog() {
		if spec.Key == key {
			return spec, true
		}
	}
	return Spec{}, false
}

// FlagForCapability returns the flag that releases a pkg/productcap
// capability, if one does.
func FlagForCapability(capability string) (string, bool) {
	for _, spec := range catalog {
		if capability != "" && spec.Capability == capability {
			return spec.Key, true
		}
	}
	return "", false
}

// ConfigKey is the runtime-configuration key that stores a flag.
func ConfigKey(flagKey string) string { return configKeyPrefix + flagKey }

// FlagKeyFromConfigKey reverses ConfigKey.
func FlagKeyFromConfigKey(configKey string) (string, bool) {
	key, ok := strings.CutPrefix(configKey, configKeyPrefix)
	return key, ok && key != ""
}

// IsConfigKey reports whether a runtime-configuration key belongs to platform
// flags (a flag row or the groups row).
func IsConfigKey(configKey string) bool {
	if configKey == GroupsConfigKey {
		return true
	}
	_, ok := FlagKeyFromConfigKey(configKey)
	return ok
}
