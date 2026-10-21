package api

import "time"

// Platform flags (ADR-1700) are operator-managed release gates. These are
// the read shapes of /v1/admin/flags and /v1/admin/accounts/{id}/flags;
// writes go through PATCH /v1/admin/config/{config_key}.

// PlatformFlagRule targets accounts. Accounts, orgs, group, plans and rollout
// combine with AND; the first matching rule decides. Orgs matches the
// organization a request acts under (ADR 20261021080640).
// StartsAt and EndsAt bound when the rule applies.
type PlatformFlagRule struct {
	ID       string            `json:"id"`
	Accounts []string          `json:"accounts,omitempty"`
	Orgs     []string          `json:"orgs,omitempty"`
	Group    string            `json:"group,omitempty"`
	Plans    []string          `json:"plans,omitempty"`
	Rollout  *int              `json:"rollout,omitempty"`
	StartsAt *time.Time        `json:"starts_at,omitempty"`
	EndsAt   *time.Time        `json:"ends_at,omitempty"`
	Ramp     *PlatformFlagRamp `json:"ramp,omitempty"`
	Value    *bool             `json:"value"`
}

// PlatformFlagRamp advances a rollout rule stage by stage while the flag's
// health routes stay healthy (ADR 20261021080640).
type PlatformFlagRamp struct {
	Stages            []int  `json:"stages"`
	IntervalSeconds   int    `json:"interval_seconds"`
	Max5xxBasisPoints int    `json:"max_5xx_basis_points"`
	MinRequests       int64  `json:"min_requests"`
	OnBreach          string `json:"on_breach"`
	Paused            bool   `json:"paused,omitempty"`
	PausedReason      string `json:"paused_reason,omitempty"`
}

// PlatformFlagDefinition is the stored value of a platform flag. Enabled is
// the kill switch; Default applies to accounts no rule matches.
type PlatformFlagDefinition struct {
	Enabled   bool                   `json:"enabled"`
	Default   bool                   `json:"default"`
	Rules     []PlatformFlagRule     `json:"rules,omitempty"`
	SelfServe *PlatformFlagSelfServe `json:"self_serve,omitempty"`
}

// PlatformFlagSelfServe lets customers join a preview themselves (ADR
// 20261021080640). Plans empty means every plan; MaxSubjects 0 means the
// platform limit.
type PlatformFlagSelfServe struct {
	Enabled     bool     `json:"enabled"`
	Plans       []string `json:"plans,omitempty"`
	MaxSubjects int      `json:"max_subjects,omitempty"`
}

// PlatformFlagOptIns is the stored value of platform_flag_opt_ins.<key>: the
// accounts and organizations that joined a self-serve preview. Operators
// write it with PATCH /v1/admin/config/platform_flag_opt_ins.<key>.
type PlatformFlagOptIns struct {
	Accounts []string `json:"accounts,omitempty"`
	Orgs     []string `json:"orgs,omitempty"`
}

// OperatorPlatformFlagOptIns counts the accounts and organizations that
// joined a self-serve preview.
type OperatorPlatformFlagOptIns struct {
	Accounts int   `json:"accounts"`
	Orgs     int   `json:"orgs"`
	Version  int64 `json:"version"`
}

// OperatorRuntimeConfigApplyReceipt is what one daemon instance reported
// applying for a runtime-configuration key.
type OperatorRuntimeConfigApplyReceipt struct {
	ConfigKey  string `json:"config_key"`
	Daemon     string `json:"daemon"`
	Instance   string `json:"instance"`
	Version    int64  `json:"version"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	ReportedAt string `json:"reported_at"`
}

// OperatorPlatformFlagConvergence compares the flag's current version with
// the receipts of the daemons that consume it. A consumer is converged when
// every instance it reported applied the current version.
type OperatorPlatformFlagConvergence struct {
	Converged []string `json:"converged"`
	Pending   []string `json:"pending"`
	Failed    []string `json:"failed"`
}

// OperatorPlatformFlag is one catalogued platform flag with its live value.
type OperatorPlatformFlag struct {
	Key         string                              `json:"key"`
	ConfigKey   string                              `json:"config_key"`
	Label       string                              `json:"label"`
	Description string                              `json:"description"`
	Capability  string                              `json:"capability,omitempty"`
	ADR         string                              `json:"adr"`
	Consumers   []string                            `json:"consumers"`
	Attributes  []string                            `json:"attributes"`
	LegacyEnv   []string                            `json:"legacy_env"`
	RetireBy    string                              `json:"retire_by,omitempty"`
	Definition  PlatformFlagDefinition              `json:"definition"`
	Source      string                              `json:"source"`
	Version     int64                               `json:"version"`
	Status      string                              `json:"status"`
	LastError   string                              `json:"last_error,omitempty"`
	UpdatedAt   string                              `json:"updated_at,omitempty"`
	Receipts    []OperatorRuntimeConfigApplyReceipt `json:"receipts"`
	Convergence OperatorPlatformFlagConvergence     `json:"convergence"`
	OptIns      OperatorPlatformFlagOptIns          `json:"opt_ins"`
}

// OperatorPlatformFlagGroups is the shared named account sets, with the
// flags that reference each group.
type OperatorPlatformFlagGroups struct {
	ConfigKey  string              `json:"config_key"`
	Groups     map[string][]string `json:"groups"`
	References map[string][]string `json:"references"`
	Version    int64               `json:"version"`
}

// OperatorPlatformFlagList is GET /v1/admin/flags.
type OperatorPlatformFlagList struct {
	Items       []OperatorPlatformFlag     `json:"items"`
	Groups      OperatorPlatformFlagGroups `json:"groups"`
	GeneratedAt string                     `json:"generated_at"`
}

// OperatorPlatformFlagDecision explains a flag for one account. Available
// is the flag decision AND the plan entitlement of the capability it
// releases; Entitled is omitted for a flag that releases no capability.
type OperatorPlatformFlagDecision struct {
	Flag      string `json:"flag"`
	AccountID string `json:"account_id"`
	OrgID     string `json:"org_id,omitempty"`
	Plan      string `json:"plan"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason"`
	RuleID    string `json:"rule_id,omitempty"`
	Bucket    *int   `json:"bucket,omitempty"`
	Version   int64  `json:"version"`
	Entitled  *bool  `json:"entitled,omitempty"`
	Available bool   `json:"available"`
	// ScheduledRuleID and ScheduleState name the first rule that matched but
	// was outside its window (rule_not_started or rule_ended).
	ScheduledRuleID string `json:"scheduled_rule_id,omitempty"`
	ScheduleState   string `json:"schedule_state,omitempty"`
	// At is the decision time when the inspector was asked about another
	// moment than now.
	At string `json:"at,omitempty"`
	// OptIn is "account" or "org" when a self-serve opt-in decided.
	OptIn string `json:"opt_in,omitempty"`
}

// OperatorAccountPlatformFlags is GET /v1/admin/accounts/{id}/flags.
type OperatorAccountPlatformFlags struct {
	AccountID string                         `json:"account_id"`
	Plan      string                         `json:"plan"`
	Items     []OperatorPlatformFlagDecision `json:"items"`
}
