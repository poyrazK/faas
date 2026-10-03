package routerequirements

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

const ServerPlanScope = "Server snapshot of current app policy and account limits. Apply recomputes this plan under transaction locks and commits all changes with a durable receipt. Results check configured policy for concrete requests; runtime admission and application behavior require separate tests."

// Normalize validates the same document semantics on the CLI and server and
// orders routes before hashing. It never trusts caller-provided plan changes.
func Normalize(config Config) (Config, string, error) {
	if config.Groups != nil || config.Public != nil {
		return Config{}, "", errors.New("version 1 requirements cannot contain groups or public exceptions")
	}
	body, err := json.Marshal(config)
	if err != nil {
		return Config{}, "", err
	}
	config, err = Parse(body)
	if err != nil {
		return Config{}, "", err
	}
	sort.Slice(config.Routes, func(i, j int) bool {
		return config.Routes[i].Method+" "+config.Routes[i].Path < config.Routes[j].Method+" "+config.Routes[j].Path
	})
	return config, jsonDigest(config), nil
}

func BuildServerPlan(config Config, context Context, options PlanOptions) (PolicyPlan, error) {
	config, digest, err := Normalize(config)
	if err != nil {
		return PolicyPlan{}, err
	}
	if options.ThrottleBurst < 0 {
		return PolicyPlan{}, errors.New("throttle_burst must be nonnegative")
	}
	if options.ConsolidateBudgets {
		return PolicyPlan{}, errors.New("budget consolidation requires version 2 route groups")
	}
	plan := BuildPlan(config, digest, context, options)
	plan.Version, plan.Authority, plan.Scope = 2, "server", ServerPlanScope
	plan.Requirements, plan.ThrottleBurst = &config, options.ThrottleBurst
	plan.SHA256 = ""
	plan.SHA256 = jsonDigest(plan)
	return plan, nil
}

// ValidateArtifact detects edited or legacy artifacts before an apply request.
// The server still recomputes the complete plan under locks.
func ValidateArtifact(plan PolicyPlan, slug string) error {
	if (plan.Version != 2 && plan.Version != 3) || plan.Authority != "server" || plan.Requirements == nil {
		return errors.New("apply requires a version 2 or 3 server plan; rerun gregale routes plan")
	}
	if plan.RequirementsRevision < 0 || plan.RequirementsRevision > api.RouteRequirementsMaxRevision || plan.RequirementsRevision > 0 && plan.Version != 3 {
		return errors.New("saved requirements revision requires a captured version 3 plan within the supported range")
	}
	if plan.RequirementsRevision > 0 {
		_, digest, err := NormalizeCoverage(*plan.Requirements)
		if err != nil || digest != plan.RequirementsSHA256 {
			return errors.New("saved requirements fingerprint does not match the reviewed intent")
		}
	}
	if plan.Version == 3 && (plan.Requirements.Version != 2 || plan.DeploymentID == "" || plan.Before.Coverage == nil || plan.Before.Coverage.Status != "available" || plan.Before.Coverage.Deployment != plan.DeploymentID || len(plan.Before.Coverage.SHA256) != 64) {
		return errors.New("group plans require a bound captured deployment contract")
	}
	if plan.Version == 2 && (plan.Requirements.Version != 1 || plan.DeploymentID != "" || plan.ConsolidateBudgets) {
		return errors.New("legacy plans require concrete version 1 requirements")
	}
	if plan.App != slug || plan.AppID == "" || (plan.Status != "ready" && plan.Status != "no_changes") {
		return errors.New("plan must belong to this app and have no unresolved requirements")
	}
	digest := plan.SHA256
	plan.SHA256 = ""
	if len(digest) != 64 || digest != jsonDigest(plan) {
		return errors.New("plan fingerprint does not match its contents; rerun gregale routes plan")
	}
	return nil
}
