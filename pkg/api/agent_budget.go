package api

// AgentStandingMillicents prices floor always-on instances of ramMB each over
// a month at the overage rate (ADR-20261111114059): what an app's floor
// commits its account to whether or not traffic arrives. Each instance is
// billed at its RAM plus the 8 MB overhead (§4.7).
func AgentStandingMillicents(floor, ramMB int) int64 {
	if floor <= 0 || ramMB <= 0 {
		return 0
	}
	mbHours := int64(floor) * int64(ramMB+8) * AgentBudgetHoursPerMonth
	return mbHours * OverageMillicentsPerGBHour / 1024
}

// AgentPrewarmMillicents prices a prewarm of count instances of ramMB each
// held for seconds: a one-off charge, not a monthly one.
func AgentPrewarmMillicents(count, ramMB int, seconds int64) int64 {
	if count <= 0 || ramMB <= 0 || seconds <= 0 {
		return 0
	}
	mbSeconds := int64(count) * int64(ramMB+8) * seconds
	return mbSeconds * OverageMillicentsPerGBHour / (1024 * 3600)
}

// AgentBudgetResponse is an agent connection's budget and what it committed
// this month (ADR-20261111114059).
type AgentBudgetResponse struct {
	// MonthlyCents is the budget that applies; absent when unlimited.
	MonthlyCents *int64 `json:"monthly_cents,omitempty"`
	// Source says where it comes from: key, plan_default, org_ceiling or
	// unlimited.
	Source string `json:"source"`
	// KeyMonthlyCents is the key's own budget, when it set one; KeyUnlimited
	// when it chose to have none.
	KeyMonthlyCents *int64 `json:"key_monthly_cents,omitempty"`
	KeyUnlimited    bool   `json:"key_unlimited,omitempty"`
	// OrgMaxCents is the organization's ceiling, when it set one.
	OrgMaxCents *int64 `json:"org_max_cents,omitempty"`
	// SpentCents is the committed cost added this UTC month, rounded up.
	SpentCents int64 `json:"spent_cents"`
	// Month is the UTC month spend counts in, as YYYY-MM.
	Month string `json:"month"`
}

// AgentBudgetRequest sets an agent connection's budget: exactly one of
// MonthlyCents, Unlimited or Default.
type AgentBudgetRequest struct {
	MonthlyCents *int64 `json:"monthly_cents,omitempty"`
	Unlimited    bool   `json:"unlimited,omitempty"`
	Default      bool   `json:"default,omitempty"`
}

// Agent budget sources.
const (
	AgentBudgetSourceKey         = "key"
	AgentBudgetSourcePlanDefault = "plan_default"
	AgentBudgetSourceOrgCeiling  = "org_ceiling"
	AgentBudgetSourceUnlimited   = "unlimited"
)

// EffectiveAgentBudget combines a key's own budget with its organization's
// ceiling and the plan default: the lower of the key's budget (the plan
// default when it set none) and the ceiling. ok is false when nothing
// limits the key.
func EffectiveAgentBudget(plan Plan, keySet, keyUnlimited bool, keyCents int64, orgMax *int64) (cents int64, source string, ok bool) {
	switch {
	case keySet && keyUnlimited:
		source = AgentBudgetSourceUnlimited
	case keySet:
		cents, source, ok = keyCents, AgentBudgetSourceKey, true
	default:
		cents, source, ok = plan.AgentBudgetDefaultCents(), AgentBudgetSourcePlanDefault, true
	}
	if orgMax != nil && (!ok || *orgMax < cents) {
		return *orgMax, AgentBudgetSourceOrgCeiling, true
	}
	return cents, source, ok
}

// CentsCeil rounds millicents up to whole cents.
func CentsCeil(millicents int64) int64 {
	if millicents <= 0 {
		return 0
	}
	return (millicents + MillicentsPerCent - 1) / MillicentsPerCent
}
