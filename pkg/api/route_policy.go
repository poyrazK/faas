// Route policy wire types shared by apid, the CLI, and SDK clients.
package api

import (
	"encoding/json"
	"time"
)

type RouteRequirementsConfig struct {
	Version int                    `json:"version" yaml:"version"`
	Routes  []RouteRequirement     `json:"routes,omitempty" yaml:"routes,omitempty"`
	Groups  []RouteGroup           `json:"groups,omitempty" yaml:"groups,omitempty"`
	Public  []RoutePublicException `json:"public,omitempty" yaml:"public,omitempty"`
}

type SavedRouteRequirements struct {
	AppID        string                  `json:"app_id"`
	Revision     int64                   `json:"revision"`
	SHA256       string                  `json:"sha256"`
	Requirements RouteRequirementsConfig `json:"requirements"`
	UpdatedAt    time.Time               `json:"updated_at"`
}

// Zero creates the first revision; every replacement compares the current one.
type SaveRouteRequirementsRequest struct {
	ExpectedRevision *int64                  `json:"expected_revision"`
	Requirements     RouteRequirementsConfig `json:"requirements"`
}

type CheckRouteRequirementsRequest struct {
	DeploymentID     string `json:"deployment_id"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

// Captured contract and current app configuration are read in one snapshot.
// This is explicit configured-policy evidence, not a runtime or deployment gate.
type RouteRequirementsCheck struct {
	Version              int                     `json:"version"`
	App                  string                  `json:"app"`
	AppID                string                  `json:"app_id"`
	DeploymentID         string                  `json:"deployment_id"`
	RequirementsRevision int64                   `json:"requirements_revision"`
	RequirementsSHA256   string                  `json:"requirements_sha256"`
	ConfigurationSHA256  string                  `json:"configuration_sha256"`
	Report               RouteRequirementsReport `json:"report"`
}

// The verdict and freshness are separate: a historical pass can be stale.
type AutomaticRouteCheck struct {
	CheckID                     string                  `json:"check_id,omitempty"`
	Changes                     *RouteCheckChanges      `json:"changes,omitempty"`
	Version                     int                     `json:"version"`
	App                         string                  `json:"app"`
	AppID                       string                  `json:"app_id"`
	DeploymentID                string                  `json:"deployment_id"`
	State                       string                  `json:"state"`
	Freshness                   string                  `json:"freshness"`
	StaleReasons                []string                `json:"stale_reasons"`
	CurrentRequirementsRevision int64                   `json:"current_requirements_revision"`
	CurrentRequirementsSHA256   string                  `json:"current_requirements_sha256"`
	Attempts                    int                     `json:"attempts"`
	LastErrorCode               string                  `json:"last_error_code,omitempty"`
	QueuedAt                    time.Time               `json:"queued_at"`
	NextAttemptAt               *time.Time              `json:"next_attempt_at,omitempty"`
	CheckedAt                   *time.Time              `json:"checked_at,omitempty"`
	Check                       *RouteRequirementsCheck `json:"check,omitempty"`
}

type RouteGroup struct {
	Name       string      `json:"name" yaml:"name"`
	PathPrefix string      `json:"path_prefix" yaml:"path_prefix"`
	Methods    []string    `json:"methods" yaml:"methods"`
	Require    RouteChecks `json:"require" yaml:"require"`
}

type RoutePublicException struct {
	Method string `json:"method" yaml:"method"`
	Path   string `json:"path" yaml:"path"`
	Reason string `json:"reason" yaml:"reason"`
}

type RouteRequirement struct {
	Name    string      `json:"name,omitempty" yaml:"name,omitempty"`
	Method  string      `json:"method" yaml:"method"`
	Path    string      `json:"path" yaml:"path"`
	Require RouteChecks `json:"require" yaml:"require"`
}

type RouteChecks struct {
	Authentication string                    `json:"authentication,omitempty" yaml:"authentication,omitempty"`
	Throttle       *RouteThrottleRequirement `json:"throttle,omitempty" yaml:"throttle,omitempty"`
	Budget         *RouteBudgetRequirement   `json:"budget,omitempty" yaml:"budget,omitempty"`
}

type RouteThrottleRequirement struct {
	KeyBy            string   `json:"key_by" yaml:"key_by"`
	MaxRPS           *float64 `json:"max_rps,omitempty" yaml:"max_rps,omitempty"`
	MissingKeyPolicy string   `json:"missing_key_policy,omitempty" yaml:"missing_key_policy,omitempty"`
}

type RouteBudgetRequirement struct {
	MaxMS    *int64 `json:"max_ms,omitempty" yaml:"max_ms,omitempty"`
	Explicit bool   `json:"explicit,omitempty" yaml:"explicit,omitempty"`
}

type RouteRequirementsReport struct {
	Version     int                       `json:"version"`
	SHA256      string                    `json:"sha256"`
	Host        string                    `json:"host,omitempty"`
	PolicyScope string                    `json:"policy_scope"`
	Status      string                    `json:"status"`
	Scope       string                    `json:"scope"`
	Routes      []RouteRequirementsResult `json:"routes"`
	Coverage    *RouteCoverageInventory   `json:"coverage,omitempty"`
	Groups      []RouteGroupResult        `json:"groups,omitempty"`
	Assignments []RouteAssignment         `json:"assignments,omitempty"`
}

type RouteCapturedOperation struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type RouteCoverageInventory struct {
	Status     string                   `json:"status"`
	Code       string                   `json:"code,omitempty"`
	Source     string                   `json:"source"`
	Deployment string                   `json:"deployment,omitempty"`
	SHA256     string                   `json:"sha256,omitempty"`
	RouteCount int                      `json:"route_count"`
	Routes     []RouteCapturedOperation `json:"-"`
}

type RouteGroupResult struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	MatchedRoutes int    `json:"matched_routes"`
	ExemptRoutes  int    `json:"exempt_routes"`
	Code          string `json:"code,omitempty"`
}

type RouteAssignment struct {
	Method string               `json:"method"`
	Path   string               `json:"path"`
	Scope  string               `json:"scope"`
	Groups []string             `json:"groups,omitempty"`
	Checks []RouteAssignedCheck `json:"checks,omitempty"`
}

// CheckIndex refers to the operation checks in the same report.
type RouteAssignedCheck struct {
	Group      string `json:"group"`
	CheckIndex int    `json:"check_index"`
}

type RouteRequirementsResult struct {
	Name   string                     `json:"name,omitempty"`
	Method string                     `json:"method"`
	Path   string                     `json:"path"`
	Status string                     `json:"status"`
	Checks []RouteRequirementsFinding `json:"checks"`
}

// Findings contain allowlisted configuration summaries, never raw actions,
// selector header values, JWT claims, or credential material.
type RouteRequirementsFinding struct {
	Requirement string   `json:"requirement"`
	Status      string   `json:"status"`
	Code        string   `json:"code"`
	Expected    string   `json:"expected"`
	Actual      string   `json:"actual,omitempty"`
	RuleIDs     []string `json:"rule_ids,omitempty"`
	Reason      string   `json:"reason"`
	NextAction  string   `json:"next_action,omitempty"`
}

type RoutePolicyPlan struct {
	RequirementsRevision int64                    `json:"requirements_revision,omitempty"`
	ConsolidateBudgets   bool                     `json:"consolidate_budgets,omitempty"`
	RuleUsage            *RoutePolicyRuleUsage    `json:"rule_usage,omitempty"`
	DeploymentID         string                   `json:"deployment_id,omitempty"`
	Authority            string                   `json:"authority,omitempty"`
	Requirements         *RouteRequirementsConfig `json:"requirements,omitempty"`
	ThrottleBurst        int                      `json:"throttle_burst,omitempty"`
	Version              int                      `json:"version"`
	App                  string                   `json:"app"`
	AppID                string                   `json:"app_id,omitempty"`
	Host                 string                   `json:"host,omitempty"`
	PlanName             string                   `json:"plan_name,omitempty"`
	Status               string                   `json:"status"`
	SHA256               string                   `json:"sha256,omitempty"`
	ConfigurationSHA256  string                   `json:"configuration_sha256"`
	RequirementsSHA256   string                   `json:"requirements_sha256"`
	Scope                string                   `json:"scope"`
	Before               RouteRequirementsReport  `json:"before"`
	After                RouteRequirementsReport  `json:"after"`
	Changes              []RoutePolicyChange      `json:"changes"`
	Unresolved           []RoutePlanUnresolved    `json:"unresolved"`
}

type RoutePolicyChange struct {
	BudgetGroup      string                 `json:"budget_group,omitempty"`
	Operation        string                 `json:"operation"`
	Kind             string                 `json:"kind"`
	RuleID           string                 `json:"rule_id,omitempty"`
	SimulatedRuleID  string                 `json:"simulated_rule_id,omitempty"`
	Create           *CreateEdgeRuleRequest `json:"create,omitempty"`
	Update           *UpdateEdgeRuleRequest `json:"update,omitempty"`
	Expected         string                 `json:"expected"`
	Before           string                 `json:"before,omitempty"`
	After            string                 `json:"after"`
	DisplacedRuleIDs []string               `json:"displaced_rule_ids,omitempty"`
	Impact           RoutePolicyImpact      `json:"impact"`
	Reason           string                 `json:"reason"`
}

// Counts include disabled rules, which also consume the app quota.
type RoutePolicyRuleUsage struct {
	Before int `json:"before"`
	After  int `json:"after"`
	Limit  int `json:"limit"`
}

type RoutePolicyImpact struct {
	Host          string                         `json:"host"`
	Method        string                         `json:"method"`
	Path          string                         `json:"path"`
	Scope         string                         `json:"scope"`
	Captured      []RoutePolicyAffectedOperation `json:"captured,omitempty"`
	BeyondCapture bool                           `json:"beyond_capture,omitempty"`
}

// Captured impact describes policy selection, independently of public intent.
type RoutePolicyAffectedOperation struct {
	Method       string `json:"method"`
	Path         string `json:"path"`
	Relation     string `json:"relation"`
	Public       bool   `json:"public,omitempty"`
	BeforeRuleID string `json:"before_rule_id,omitempty"`
	AfterRuleID  string `json:"after_rule_id,omitempty"`
}

type RoutePlanUnresolved struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Requirement string `json:"requirement"`
	Status      string `json:"status"`
	Code        string `json:"code"`
	Reason      string `json:"reason"`
	NextAction  string `json:"next_action"`
}

type RoutePolicyPlanRequest struct {
	Saved              bool                    `json:"saved,omitempty"`
	ExpectedRevision   *int64                  `json:"expected_revision,omitempty"`
	ConsolidateBudgets bool                    `json:"consolidate_budgets,omitempty"`
	DeploymentID       string                  `json:"deployment_id,omitempty"`
	Requirements       RouteRequirementsConfig `json:"requirements"`
	ThrottleBurst      int                     `json:"throttle_burst,omitempty"`
}
type RoutePolicyApplyRequest struct {
	RoutePolicyPlanRequest
	ExpectedPlanSHA256 string `json:"expected_plan_sha256"`
	Confirm            bool   `json:"confirm"`
}

// Preserve the value-based Go API while omitting absent inline intent. The
// explicit marshaler also supports the SDK's Go 1.23 baseline, before omitzero.
type routePolicyRequestWire struct {
	Saved              bool                     `json:"saved,omitempty"`
	ExpectedRevision   *int64                   `json:"expected_revision,omitempty"`
	ConsolidateBudgets bool                     `json:"consolidate_budgets,omitempty"`
	DeploymentID       string                   `json:"deployment_id,omitempty"`
	Requirements       *RouteRequirementsConfig `json:"requirements,omitempty"`
	ThrottleBurst      int                      `json:"throttle_burst,omitempty"`
}

func (r RoutePolicyPlanRequest) wire() routePolicyRequestWire {
	var config *RouteRequirementsConfig
	if r.Requirements.Version != 0 || r.Requirements.Routes != nil || r.Requirements.Groups != nil || r.Requirements.Public != nil {
		config = &r.Requirements
	}
	return routePolicyRequestWire{r.Saved, r.ExpectedRevision, r.ConsolidateBudgets, r.DeploymentID, config, r.ThrottleBurst}
}

func (r RoutePolicyPlanRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.wire())
}

func (r RoutePolicyApplyRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		routePolicyRequestWire
		ExpectedPlanSHA256 string `json:"expected_plan_sha256"`
		Confirm            bool   `json:"confirm"`
	}{r.wire(), r.ExpectedPlanSHA256, r.Confirm})
}

type RoutePolicyAppliedChange struct {
	Operation string `json:"operation"`
	Kind      string `json:"kind"`
	RuleID    string `json:"rule_id"`
}

// The receipt records committed configuration. Gateway convergence is a
// separate observation and does not change this durable record.
type RoutePolicyReceipt struct {
	ID           string                     `json:"id"`
	AppID        string                     `json:"app_id"`
	PlanSHA256   string                     `json:"plan_sha256"`
	AppliedAt    time.Time                  `json:"applied_at"`
	Changes      []RoutePolicyAppliedChange `json:"changes"`
	Verification RouteRequirementsReport    `json:"verification"`
}
type RoutePolicyApplyResponse struct {
	Receipt           RoutePolicyReceipt `json:"receipt"`
	Replayed          bool               `json:"replayed"`
	GatewayState      string             `json:"gateway_state"`
	GatewayGeneration int64              `json:"gateway_generation,omitempty"`
}
