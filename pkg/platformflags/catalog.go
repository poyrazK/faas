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
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	OperationSchedules   = "operation_schedules"
	OperationChildren    = "operation_children"
	ServiceMap           = "service_map"
	GuestTracing         = "guest_tracing"
	StaticEgressIP       = "static_egress_ip"
	RealtimeResume       = "realtime_resume"
	RealtimeTickets      = "realtime_tickets"
	RealtimeAccess       = "realtime_access"
	RealtimeOccupancy    = "realtime_occupancy"
	StrictBudgets        = "strict_budgets"
)

// RealtimePreviews are the flags apid decides per endpoint and sends to
// realtimed with the endpoint's registration (ADR 20261021080640).
var RealtimePreviews = []string{RealtimeResume, RealtimeTickets, RealtimeAccess, RealtimeOccupancy}

// Attribute is a subject attribute a rule can constrain beyond the account,
// which every consumer always knows (ADR 20261021080640).
type Attribute string

const (
	// AttributePlan means every consumer knows the account's plan.
	AttributePlan Attribute = "plan"
	// AttributeOrg means every consumer knows the organization the request
	// acts under.
	AttributeOrg Attribute = "org"
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
	// Attributes are the subject attributes every consumer can supply. A
	// rule that constrains any other attribute is refused when written, so it
	// can never silently match nobody.
	Attributes []Attribute
	// RetireBy is the date (YYYY-MM-DD) by which a preview flag should be
	// removed or folded into general availability; empty for a kill switch
	// with no planned end.
	RetireBy string
	// LegacyEnv names the environment variables read as the boot fallback
	// before an operator has written a value.
	LegacyEnv []string
	// HealthRoutes are the apid route patterns whose error rate judges an
	// automatic ramp of the flag. A flag without them refuses a ramp.
	HealthRoutes []string
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
		Attributes:  []Attribute{AttributePlan},
		LegacyEnv:   []string{"FAAS_AI_GATEWAY_ENABLED", "FAAS_AI_GATEWAY_ACCOUNTS"},
		RetireBy:    "2027-01-31",
	},
	{
		Key:          CustomerFlags,
		Label:        "Customer Flags",
		Description:  "Release customer feature flags (configuration, runtime reads and rollout automation) to accounts.",
		Capability:   "feature-flags",
		ADR:          "ADR-377",
		Consumers:    []string{"apid"},
		Attributes:   []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:    []string{"FAAS_FLAGS_ENABLED"},
		HealthRoutes: []string{"GET /v1/projects/{slug}/environments/{environment}/flags", "PUT /v1/projects/{slug}/environments/{environment}/flags", "POST /v1/projects/{slug}/environments/{environment}/flags/{key}/inspect"},
	},
	{
		Key:         ApplicationAuth,
		Label:       "Application Auth",
		Description: "Release application Auth (the Auth screens and the auth credential mode) to accounts. Apps already using Auth keep running while it is off.",
		ADR:         "ADR-1190",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:   []string{"FAAS_APPLICATION_AUTH_ENABLED", "FAAS_APPLICATION_AUTH_ACCOUNTS"},
		RetireBy:    "2027-01-31",
	},
	{
		Key:         LiveQueries,
		Label:       "Live queries",
		Description: "Release live queries (incremental SQL results over realtime) to accounts. Decided by account only, because the realtime path knows nothing else, so plan and org rules are refused. Unless every account is included, the live-query routes stay hidden from accounts outside it.",
		ADR:         "ADR-20261016151947",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_LIVE_QUERIES_ACCOUNTS"},
		RetireBy:    "2027-01-31",
	},
	{
		Key:          ChangeTimeline,
		Label:        "Change timeline",
		Description:  "Release the app change timeline preview (GET /v1/apps/{slug}/changes and its dashboard page) to accounts.",
		ADR:          "ADR-751",
		Consumers:    []string{"apid"},
		Attributes:   []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:    []string{"FAAS_CHANGE_TIMELINE_ENABLED"},
		HealthRoutes: []string{"GET /v1/apps/{slug}/changes"},
		RetireBy:     "2027-01-31",
	},
	{
		Key:         AnomalyAlerts,
		Label:       "Anomaly alerts",
		Description: "Release baseline alert comparisons (above_baseline, below_baseline) to accounts. Existing rules keep evaluating while it is off; only creating or changing one is refused.",
		ADR:         "ADR-744",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:   []string{"FAAS_ANOMALY_ALERTS_ENABLED"},
		RetireBy:    "2027-01-31",
	},
	{
		Key:         TriggerBatchDelivery,
		Label:       "Trigger batch delivery",
		Description: "Release batch delivery (delivery: batch) on triggers to accounts. Triggers that already deliver in batches keep doing so while it is off; only creating one or switching one to batch is refused.",
		ADR:         "ADR-1321",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:   []string{"FAAS_TRIGGER_BATCH_DELIVERY_PREVIEW_ENABLED"},
		RetireBy:    "2027-01-31",
	},
	{
		Key:          OperationSchedules,
		Label:        "Operation schedules",
		Description:  "Release Operation schedules to accounts: the schedule routes, and firing of their schedules. Decided by account only, because the schedule firer knows nothing else.",
		ADR:          "ADR-20261018161833",
		Consumers:    []string{"apid"},
		LegacyEnv:    []string{"FAAS_OPERATION_SCHEDULES_ACCOUNTS"},
		HealthRoutes: []string{"GET /v1/apps/{slug}/operation-schedules", "POST /v1/apps/{slug}/operation-schedules/{id}/cancel", "GET /v1/platform-tenant-self/customer-operation-schedules", "POST /v1/platform-tenant-self/customer-operation-schedules"},
	},
	{
		Key:          OperationChildren,
		Label:        "Child Operations",
		Description:  "Release child Operations to accounts: starting children and cascading a parent's cancellation to them. Decided by account only.",
		ADR:          "ADR-20261018183000",
		Consumers:    []string{"apid"},
		LegacyEnv:    []string{"FAAS_OPERATION_CHILDREN_ACCOUNTS"},
		HealthRoutes: []string{"GET /v1/apps/{slug}/operations/{id}/children", "GET /v1/platform-tenant-self/customer-operations/{id}/children"},
	},
	{
		Key:          ServiceMap,
		Label:        "Service map",
		Description:  "Release the service map preview (GET /v1/service-map and its dashboard page) to accounts. The plan check for per-app metrics still applies.",
		ADR:          "ADR-1131",
		Consumers:    []string{"apid"},
		Attributes:   []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:    []string{"FAAS_SERVICE_MAP_ENABLED"},
		HealthRoutes: []string{"GET /v1/service-map"},
		RetireBy:     "2027-01-31",
	},
	{
		Key:         GuestTracing,
		Label:       "Guest request tracing",
		Description: "Release in-guest request tracing to accounts: apps may turn tracing on. vmmd's trace receiver still needs FAAS_GUEST_TRACING_ENABLED on the node; the flag only releases the setting.",
		ADR:         "ADR-958",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan},
		LegacyEnv:   []string{"FAAS_GUEST_TRACING_ENABLED"},
	},
	{
		Key:          StaticEgressIP,
		Label:        "Static egress IP",
		Description:  "Release static egress IP management for apps to accounts.",
		ADR:          "ADR-119",
		Consumers:    []string{"apid"},
		Attributes:   []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:    []string{"FAAS_STATIC_EGRESS_IP_ENABLED"},
		HealthRoutes: []string{"GET /v1/apps/{slug}/static-egress-ip", "PUT /v1/apps/{slug}/static-egress-ip", "DELETE /v1/apps/{slug}/static-egress-ip"},
	},
	{
		Key:         RealtimeResume,
		Label:       "Realtime resume protocol",
		Description: "Release the v2 resumable realtime protocol (history, presence, signals) to accounts. apid sends the decision with each endpoint's registration; a realtime node also needs FAAS_REALTIME_RESUME_PREVIEW_ENABLED. Takes effect for new connections within the 30-second endpoint reconcile.",
		ADR:         "ADR-369",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:   []string{"FAAS_REALTIME_RESUME_PREVIEW_ENABLED"},
		RetireBy:    "2027-01-31",
	},
	{
		Key:         RealtimeTickets,
		Label:       "Realtime subscription tickets",
		Description: "Release short-lived channel subscription tickets to accounts: the ticket API on apid and ticket connections on realtime nodes, which also need FAAS_REALTIME_TICKETS_PREVIEW_ENABLED.",
		ADR:         "ADR-1117",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:   []string{"FAAS_REALTIME_TICKETS_PREVIEW_ENABLED"},
		RetireBy:    "2027-01-31",
		HealthRoutes: []string{
			"POST /v1/apps/{slug}/realtime/endpoints/{id}/tickets", "DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets",
			"DELETE /v1/apps/{slug}/realtime/endpoints/{id}/tickets/{ticket}",
		},
	},
	{
		Key:         RealtimeAccess,
		Label:       "Realtime access revocation",
		Description: "Release durable principal channel and session revocation to accounts: the access API on apid and its enforcement on realtime nodes, which also need FAAS_REALTIME_ACCESS_PREVIEW_ENABLED.",
		ADR:         "ADR-1113",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:   []string{"FAAS_REALTIME_ACCESS_PREVIEW_ENABLED"},
		RetireBy:    "2027-01-31",
		HealthRoutes: []string{
			"GET /v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access",
			"POST /v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access",
		},
	},
	{
		Key:          RealtimeOccupancy,
		Label:        "Realtime channel occupancy",
		Description:  "Release channel occupancy to accounts: the occupancy API on apid and live occupancy frames and SSE events on realtime nodes, which also need FAAS_REALTIME_OCCUPANCY_PREVIEW_ENABLED.",
		ADR:          "ADR-1324",
		Consumers:    []string{"apid"},
		Attributes:   []Attribute{AttributePlan, AttributeOrg},
		LegacyEnv:    []string{"FAAS_REALTIME_OCCUPANCY_PREVIEW_ENABLED"},
		RetireBy:     "2027-01-31",
		HealthRoutes: []string{"GET /v1/apps/{slug}/realtime/endpoints/{id}/occupancy"},
	},
	{
		Key:         StrictBudgets,
		Label:       "Strict budgets",
		Description: "Release strict (suspend_workloads) compute and egress budgets to accounts. Off by default until native acceptance passes. Strict budgets that are already enabled keep enforcing and stay editable while it is off; only creating or enabling one is refused.",
		ADR:         "ADR-1750",
		Consumers:   []string{"apid"},
		Attributes:  []Attribute{AttributePlan},
		RetireBy:    "2027-01-31",
	},
}

// Catalog returns every declared platform flag in a stable order.
func Catalog() []Spec {
	out := make([]Spec, len(catalog))
	for i, spec := range catalog {
		spec.Consumers = slices.Clone(spec.Consumers)
		spec.Attributes = slices.Clone(spec.Attributes)
		spec.LegacyEnv = slices.Clone(spec.LegacyEnv)
		spec.HealthRoutes = slices.Clone(spec.HealthRoutes)
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

// RetireByTime is RetireBy as the start of that day in UTC.
func (s Spec) RetireByTime() (time.Time, bool) {
	if s.RetireBy == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.DateOnly, s.RetireBy)
	return at, err == nil
}

// Supports reports whether every consumer of the flag can supply attr.
func (s Spec) Supports(attr Attribute) bool { return slices.Contains(s.Attributes, attr) }

// ValidateTargeting refuses rules that constrain an attribute some consumer
// of the flag cannot supply: such a rule would match nobody wherever that
// consumer decides, without telling the operator.
func ValidateTargeting(spec Spec, def Definition) error {
	for _, rule := range def.Rules {
		if len(rule.Plans) > 0 && !spec.Supports(AttributePlan) {
			return fmt.Errorf("rule %q targets plans, but %s is decided where the plan is unknown (%s)", rule.ID, spec.Key, strings.Join(spec.Consumers, ", "))
		}
		if len(rule.Orgs) > 0 && !spec.Supports(AttributeOrg) {
			return fmt.Errorf("rule %q targets organizations, but %s is decided where the organization is unknown (%s)", rule.ID, spec.Key, strings.Join(spec.Consumers, ", "))
		}
	}
	if def.SelfServe != nil {
		return validateSelfServe(spec, *def.SelfServe)
	}
	return nil
}

// validateSelfServe keeps a self-serve preview from implying an entitlement:
// a flag that releases a paid capability may only be offered to plans that
// include it, which needs the plan where the flag is decided.
func validateSelfServe(spec Spec, selfServe SelfServe) error {
	if len(selfServe.Plans) > 0 && !spec.Supports(AttributePlan) {
		return fmt.Errorf("self_serve limits plans, but %s is decided where the plan is unknown (%s)", spec.Key, strings.Join(spec.Consumers, ", "))
	}
	if spec.Capability == "" || !selfServe.Enabled {
		return nil
	}
	if len(selfServe.Plans) == 0 {
		return fmt.Errorf("self_serve for %s must list the plans entitled to %s", spec.Key, spec.Capability)
	}
	for _, plan := range selfServe.Plans {
		if entitled, err := api.CapabilityEntitled(spec.Capability, api.Plan(plan)); err != nil || !entitled {
			return fmt.Errorf("self_serve for %s offers the %s plan, which is not entitled to %s", spec.Key, plan, spec.Capability)
		}
	}
	return nil
}

// ConfigKey is the runtime-configuration key that stores a flag.
func ConfigKey(flagKey string) string { return configKeyPrefix + flagKey }

// FlagKeyFromConfigKey reverses ConfigKey.
func FlagKeyFromConfigKey(configKey string) (string, bool) {
	key, ok := strings.CutPrefix(configKey, configKeyPrefix)
	return key, ok && key != ""
}

// IsConfigKey reports whether a runtime-configuration key belongs to platform
// flags (a flag row, an opt-in row or the groups row).
func IsConfigKey(configKey string) bool {
	if configKey == GroupsConfigKey {
		return true
	}
	if _, ok := FlagKeyFromOptInsConfigKey(configKey); ok {
		return true
	}
	_, ok := FlagKeyFromConfigKey(configKey)
	return ok
}
