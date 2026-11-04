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
	APIMonetization      = "api_monetization"
	RealtimePreview      = "realtime_preview"

	OperationSchedules = "operation_schedules"
	OperationChildren  = "operation_children"
	ServiceMap         = "service_map"
	GuestTracing       = "guest_tracing"
	StaticEgressIP     = "static_egress_ip"
	RealtimeResume     = "realtime_resume"
	RealtimeTickets    = "realtime_tickets"
	RealtimeAccess     = "realtime_access"
	RealtimeOccupancy  = "realtime_occupancy"
	StrictBudgets      = "strict_budgets"

	// QuietPauseUnbilled releases unbilled quiet pause (ADR-20261023171905)
	// to accounts where meterd's quiet_pause_unbilled mode is on.
	QuietPauseUnbilled = "quiet_pause_unbilled"

	// TrafficRevocationFence is the ADR-570 security revocation fence kill
	// switch: on by default, an operator turns it off for an account or the
	// platform when the fence itself refuses healthy traffic.
	TrafficRevocationFence = "traffic_revocation_fence"

	// Edge rule features released by platform flags (ADR-20261023113721).
	EdgeWAFBlocking = "edge_waf_blocking"
	EdgeChallenge   = "edge_challenge"
	EdgePenaltyBox  = "edge_penalty_box"
	EdgeAttackMode  = "edge_attack_mode"
	EdgeAICrawlers  = "edge_ai_crawlers"
)

// EdgeFeatures are the edge rule features apid refuses to configure and
// gatewayd-internal stops enforcing while their flag is off for an account.
var EdgeFeatures = []string{EdgeWAFBlocking, EdgeChallenge, EdgePenaltyBox, EdgeAttackMode, EdgeAICrawlers}

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
	// Core marks a platform-core capability flag: writing it off needs an
	// explicit force (ADR-20261023115741).
	Core bool
	// Generated reports a flag derived from the product capability catalog
	// rather than declared by hand.
	Generated bool
	// Parent is the capability whose flag must also be on: a sub-feature
	// flag never releases more than its parent does.
	Parent string
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
		Capability:  "application-auth",
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
		Capability:  "live-queries",
		Label:       "Live queries",
		Description: "Release live queries (incremental SQL results over realtime) to accounts. Decided by account only, because the realtime path knows nothing else, so plan and org rules are refused. Unless every account is included, the live-query routes stay hidden from accounts outside it.",
		ADR:         "ADR-20261016151947",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_LIVE_QUERIES_ACCOUNTS"},
		RetireBy:    "2027-01-31",
	},
	{
		Key:          ChangeTimeline,
		Capability:   "change-timeline",
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
		Capability:  "anomaly-alerts",
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
		Capability:   "operation-schedules",
		Label:        "Operation schedules",
		Description:  "Release Operation schedules to accounts: the schedule routes, and firing of their schedules. Decided by account only, because the schedule firer knows nothing else.",
		ADR:          "ADR-20261018161833",
		Consumers:    []string{"apid"},
		LegacyEnv:    []string{"FAAS_OPERATION_SCHEDULES_ACCOUNTS"},
		HealthRoutes: []string{"GET /v1/apps/{slug}/operation-schedules", "POST /v1/apps/{slug}/operation-schedules/{id}/cancel", "GET /v1/platform-tenant-self/customer-operation-schedules", "POST /v1/platform-tenant-self/customer-operation-schedules"},
	},
	{
		Key:          OperationChildren,
		Capability:   "operation-children",
		Label:        "Child Operations",
		Description:  "Release child Operations to accounts: starting children and cascading a parent's cancellation to them. Decided by account only.",
		ADR:          "ADR-20261018183000",
		Consumers:    []string{"apid"},
		LegacyEnv:    []string{"FAAS_OPERATION_CHILDREN_ACCOUNTS"},
		HealthRoutes: []string{"GET /v1/apps/{slug}/operations/{id}/children", "GET /v1/platform-tenant-self/customer-operations/{id}/children"},
	},
	{
		Key:          ServiceMap,
		Capability:   "service-map",
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
		Capability:   "static-egress-ip",
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
		Capability:  "realtime-tickets",
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
		Capability:  "realtime-access",
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
		Capability:   "realtime-occupancy",
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
		Key:        APIMonetization,
		Capability: "api-monetization",
		Label:      "API monetization",
		Description: "Release API monetization (rate cards, plans, statements, credits, invoice exports, billing runs and their gateway enforcement) " +
			"to accounts. The api_monetization_enabled runtime switch still releases it to every account. Unless every account is included, " +
			"its routes answer an unregistered route to accounts outside the flag after authentication.",
		ADR:       "ADR-20261020170649",
		Consumers: []string{"apid", "gatewayd-internal"},
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
	{
		Key:   QuietPauseUnbilled,
		Label: "Unbilled quiet pause",
		Description: "Bill the seconds a VM runs, not the seconds it is quiet-paused between requests, for the accounts it releases. " +
			"It takes effect only where meterd's quiet_pause_unbilled mode is on; meterd counts every other account's paused seconds as in shadow mode and bills them in full. " +
			"Off by default: release it to internal accounts first, then to every account after the financial-model sign-off.",
		ADR:        "ADR-20261023171905",
		Consumers:  []string{"meterd"},
		Attributes: []Attribute{AttributePlan, AttributeOrg},
		RetireBy:   "2027-06-30",
	},
	{
		Key:        EdgeWAFBlocking,
		Capability: "edge-waf-blocking",
		Label:      "Edge WAF warn and block",
		Description: "Release warn and block modes of WAF edge rules (kind=waf) to accounts. Off by default until launch; observe mode is not gated. " +
			"While it is off apid refuses creating a warn or block rule or switching a rule to one, and gateways inspect existing warn and block rules in observe mode only.",
		ADR:        "ADR-20261023113721",
		Consumers:  []string{"apid", "gatewayd-internal"},
		Attributes: []Attribute{AttributePlan},
		RetireBy:   "2027-03-31",
	},
	{
		Key:        EdgeChallenge,
		Capability: "edge-browser-challenge",
		Label:      "Edge challenge rules",
		Description: "Release challenge edge rules (kind=challenge, with difficulty escalation) to accounts. Off by default until launch. " +
			"While it is off apid refuses creating a challenge rule or switching one on, and gateways serve no challenge pages, so existing rules are inert.",
		ADR:        "ADR-20261023113721",
		Consumers:  []string{"apid", "gatewayd-internal"},
		Attributes: []Attribute{AttributePlan},
		RetireBy:   "2027-03-31",
	},
	{
		Key:        EdgePenaltyBox,
		Capability: "edge-penalty-box",
		Label:      "Edge penalty box",
		Description: "Release edge rule penalties (penalty box) to accounts. Off by default until launch. " +
			"While it is off apid refuses adding a penalty to a rule or switching on a rule that has one, and gateways neither box clients nor refuse boxed ones; the rules themselves keep enforcing.",
		ADR:        "ADR-20261023113721",
		Consumers:  []string{"apid", "gatewayd-internal"},
		Attributes: []Attribute{AttributePlan},
		RetireBy:   "2027-03-31",
	},
	{
		Key:        EdgeAttackMode,
		Capability: "edge-attack-mode",
		Label:      "Edge attack mode",
		Description: "Release attack mode (an app-wide managed challenge rule) to accounts. Off by default until launch. Attack mode is a challenge rule, " +
			"so switching it on also needs edge_challenge. While it is off apid refuses switching attack mode on (switching it off always works), " +
			"and gateways leave an active attack-mode rule inert; other challenge rules are unaffected.",
		ADR:        "ADR-20261021220456",
		Consumers:  []string{"apid", "gatewayd-internal"},
		Attributes: []Attribute{AttributePlan},
		RetireBy:   "2027-03-31",
	},
	{
		Key:        EdgeAICrawlers,
		Capability: "edge-ai-crawler-controls",
		Label:      "Edge AI crawler conditions",
		Description: "Release the ai_crawler, ai_crawler_purpose and verified_ai_crawler edge-rule conditions (and the control-ai-crawlers recipe) to accounts. " +
			"Off by default until launch. While it is off apid refuses creating a rule that tests them or switching one on, and gateways evaluate them as absent; " +
			"AI crawler counts in the edge-protection summary and the crawler range refresh are not gated.",
		ADR:        "ADR-20261022083601",
		Consumers:  []string{"apid", "gatewayd-internal"},
		Attributes: []Attribute{AttributePlan},
		RetireBy:   "2027-03-31",
	},
	{
		Key:         RealtimePreview,
		Label:       "Realtime preview",
		Description: "Release the realtime preview (retained history and what builds on it, tickets, access controls, occupancy, usage, endpoint slugs and channel rules, the v2 protocol and SSE) to accounts. Each feature also needs its daemon flag, and the v2 protocol, tickets, access controls and occupancy also need realtime_resume, realtime_tickets, realtime_access and realtime_occupancy. Decided by account only: plan rules never match. Unless every account is included, the served OpenAPI document leaves the preview out.",
		ADR:         "ADR-20261019102657",
		Consumers:   []string{"apid"},
		LegacyEnv:   []string{"FAAS_REALTIME_PREVIEW_ACCOUNTS"},
	},
	{
		Key:   TrafficRevocationFence,
		Label: "Traffic revocation fence",
		Description: "Kill switch for the ADR-570 security revocation fence. On (the default), gateways verify the account, app and deployment " +
			"security generations before serving and cancel exchanges when one is revoked. Turned off for an account or platform-wide, " +
			"gatewayd-internal serves that account's requests without the fence and marks their answers so the public edge passes them " +
			"unverified: suspension, deletion and deployment quarantine then stop cutting in-flight traffic for those accounts. " +
			"Disable the flag to turn the fence off platform-wide; a value-false account rule turns it off for that account.",
		ADR:       "ADR-570",
		Consumers: []string{"gatewayd-internal"},
		Default:   Definition{Enabled: true, Default: true},
	},
}

// Catalog returns every declared platform flag in a stable order: the
// hand-written flags, then one generated flag per product capability that no
// hand-written flag claims.
func Catalog() []Spec {
	all := append(slices.Clip(catalog), generatedSpecs()...)
	out := make([]Spec, len(all))
	for i, spec := range all {
		spec.Consumers = slices.Clone(spec.Consumers)
		spec.Attributes = slices.Clone(spec.Attributes)
		spec.LegacyEnv = slices.Clone(spec.LegacyEnv)
		spec.HealthRoutes = slices.Clone(spec.HealthRoutes)
		spec.Default = NormalizeDefinition(spec.Default)
		if spec.Parent == "" && spec.Capability != "" {
			spec.Parent = capabilityParents()[spec.Capability]
		}
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

// FlagForCapability returns the hand-written flag that releases a
// pkg/productcap capability, if one does. CapabilityFlag also covers the
// generated flags.
func FlagForCapability(capability string) (string, bool) {
	for _, spec := range catalog {
		if capability != "" && spec.Capability == capability {
			return spec.Key, true
		}
	}
	return "", false
}

// CapabilityFlag returns the flag that gates a pkg/productcap capability:
// its hand-written flag if one claims it, else its generated flag.
func CapabilityFlag(capability string) (string, bool) {
	if key, ok := FlagForCapability(capability); ok {
		return key, true
	}
	for _, spec := range generatedSpecs() {
		if spec.Capability == capability {
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
