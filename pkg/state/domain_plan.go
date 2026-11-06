package state

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/domainplan"
)

// DomainPlanInput contains HTTP-authorized, resolved targets. The store repeats
// ownership, environment, deployment and quota checks under its write fence.
type DomainPlanInput struct {
	Manifest               api.DomainManifest
	Targets                []api.DomainBindingTarget
	AccountID, OrgID       string
	AppLimit, AccountLimit int
}

type DomainPlanStore interface {
	PlanDomainBindings(context.Context, DomainPlanInput) (api.DomainPlan, error)
	ApplyDomainBindings(context.Context, DomainPlanInput, string) (api.DomainPlan, []CustomDomain, error)
}

type domainPlanDeployment struct {
	ID      string
	Traffic int
}
type domainPlanTarget struct {
	App, AppID, AccountID, OrgID, Status, Visibility, Preview, WorkloadClass, ProjectID string
	ExecutionMode                                                                       string
	Environment, EnvironmentID, ReleaseID                                               string
	Deployments                                                                         []domainPlanDeployment
}
type domainPlanClaim struct {
	Claim            CustomDomain
	Before           api.DomainBindingTarget
	AccountID, OrgID string
	Default          bool
	TenantOverride   bool
}
type domainPlanCapture struct {
	// Only stable claim identity is hashed; polling and certificate renewals do
	// not invalidate a review. Verification loss is always significant.
	Token, BindingVersion, AppID, EnvironmentID string
	Verified                                    bool
	Default                                     bool
	Target                                      domainPlanTarget
}

func validateDomainPlanInput(in DomainPlanInput) error {
	m, err := domainplan.Normalize(in.Manifest)
	if err != nil || !slices.Equal(m.Domains, in.Manifest.Domains) || len(in.Targets) != len(m.Domains) || in.AccountID == "" || in.AppLimit < 1 || in.AccountLimit < 1 {
		return ErrInvalidArgument
	}
	for i, d := range m.Domains {
		if err := ValidateCustomDomainClaim(d.Domain); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}
		if in.Targets[i].AppID == "" || in.Targets[i].App != d.App || in.Targets[i].Environment != d.Environment {
			return ErrInvalidArgument
		}
	}
	return nil
}

func buildDomainBindingPlan(in DomainPlanInput, targets []domainPlanTarget, claims []domainPlanClaim) (api.DomainPlan, error) {
	p := api.DomainPlan{Version: 1, Manifest: in.Manifest, Changes: make([]api.DomainBindingChange, 0, len(in.Targets))}
	captures := make([]domainPlanCapture, 0, len(in.Targets))
	for i, spec := range in.Manifest.Domains {
		t, c := targets[i], claims[i]
		if t.AppID != in.Targets[i].AppID || t.AccountID != in.AccountID || t.OrgID != in.OrgID || t.EnvironmentID != in.Targets[i].EnvironmentID || t.App != spec.App || t.Status != string(AppActive) || t.Preview != "" || t.Visibility == string(api.AppVisibilityInternal) || t.WorkloadClass == string(WorkloadClassJob) || t.WorkloadClass == string(WorkloadClassWorker) || t.ExecutionMode == string(api.ExecutionModeWorker) || t.ExecutionMode == string(api.ExecutionModeJob) {
			return p, fmt.Errorf("%w: %s needs an active public destination in the same ownership scope", ErrConflict, spec.Domain)
		}
		change := api.DomainBindingChange{Domain: spec.Domain, Action: "create", After: in.Targets[i]}
		if c.Claim.Domain != "" {
			if c.AccountID != in.AccountID || c.OrgID != in.OrgID {
				return p, ErrNotFound
			}
			change.Before = &c.Before
			change.Verified = c.Claim.Verified()
			change.Action = "noop"
			if c.Claim.AppID != t.AppID || c.Claim.EnvironmentID != t.EnvironmentID {
				change.Action = "move"
				if c.TenantOverride {
					return p, fmt.Errorf("%w: %s is selected by tenant routing; update its tenant surface explicitly", ErrConflict, spec.Domain)
				}
				if !c.Claim.Verified() {
					return p, fmt.Errorf("%w: verify %s before moving its binding", ErrConflict, spec.Domain)
				}
				if len(t.Deployments) == 0 {
					return p, fmt.Errorf("%w: %s has no serving deployment in the destination", ErrConflict, spec.Domain)
				}
				change.ClearsDefault = c.Default
			}
		}
		for _, d := range t.Deployments {
			change.DeploymentIDs = append(change.DeploymentIDs, d.ID)
		}
		p.Changes = append(p.Changes, change)
		captures = append(captures, domainPlanCapture{Token: c.Claim.ChallengeToken, BindingVersion: c.Claim.BindingVersion, AppID: c.Claim.AppID, EnvironmentID: c.Claim.EnvironmentID, Verified: c.Claim.Verified(), Default: c.Default, Target: t})
	}
	var err error
	p.SHA256, err = domainplan.Fingerprint(p, struct {
		Account, Org string
		Captures     []domainPlanCapture
	}{in.AccountID, in.OrgID, captures})
	return p, err
}

func checkDomainPlanBudget(in DomainPlanInput, plan api.DomainPlan, pending map[string]int) error {
	total := 0
	for _, n := range pending {
		total += n
	}
	for _, c := range plan.Changes {
		if c.Action != "create" {
			continue
		}
		pending[c.After.AppID]++
		total++
		if pending[c.After.AppID] > in.AppLimit {
			return &CustomDomainQuotaError{"app", in.AppLimit}
		}
		if total > in.AccountLimit {
			return &CustomDomainQuotaError{"account", in.AccountLimit}
		}
	}
	return nil
}

func newDomainPlanClaim(change api.DomainBindingChange, now time.Time, token string) CustomDomain {
	return CustomDomain{Domain: change.Domain, AppID: change.After.AppID, EnvironmentID: change.After.EnvironmentID, ChallengeToken: token, CertStatus: CustomDomainCertPending, VerificationNextCheckAt: now, VerificationExpiresAt: now.Add(7 * 24 * time.Hour)}
}
