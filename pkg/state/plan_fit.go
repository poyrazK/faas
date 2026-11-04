package state

import (
	"context"
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlanSubjectAppStore is the read surface PlanSubjectApps needs.
type PlanSubjectAppStore interface {
	ListApps(ctx context.Context, accountID string) ([]App, error)
	OrgByPersonalAccount(ctx context.Context, accountID string) (Org, error)
}

// PlanSubjectApps lists the apps a billing subject's plan limits count: an
// organization's apps when the subject is an organization that owns its
// resources, else the account's personal apps (no organization, or its
// personal organization).
func PlanSubjectApps(ctx context.Context, store PlanSubjectAppStore, subject Account) ([]App, error) {
	if subject.ResourceOrgID != "" {
		lister, ok := store.(OrgAppLister)
		if !ok {
			return nil, errors.New("state: store cannot list organization apps")
		}
		return lister.ListAppsByOrg(ctx, subject.ResourceOrgID)
	}
	apps, err := store.ListApps(ctx, subject.ID)
	if err != nil {
		return nil, err
	}
	personalOrg := ""
	if org, err := store.OrgByPersonalAccount(ctx, subject.ID); err == nil {
		personalOrg = org.ID
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	personal := make([]App, 0, len(apps))
	for _, app := range apps {
		if app.OrgID == "" || app.OrgID == personalOrg {
			personal = append(personal, app)
		}
	}
	return personal, nil
}

// PlanOverage says why an app is outside its billing subject's plan.
type PlanOverage struct {
	// RAMMB is set when the app's RAM is above the plan's per-app RAM.
	RAMMB int
	// DeployedApps is set when the app ranks past the plan's deployed-app
	// limit: the subject's deployed-app count.
	DeployedApps int
}

// AppsOverPlan returns the subject's apps that are outside limits, keyed by
// app ID (ADR-20261103131742). Apps are checked when written, so a plan that
// ends or shrinks (a lapsed subscription, a provider-side plan change) left
// every app running with the higher plan's entitlements. A deployed app
// (active or parked; developer and pull-request preview apps have their own
// limits) ranks oldest first, and those past limits.DeployedApps are over the
// plan; any app whose RAM is above limits.RAMMB is over the plan until its
// RAM fits.
func AppsOverPlan(apps []App, limits api.Limits) map[string]PlanOverage {
	deployed := make([]App, 0, len(apps))
	for _, app := range apps {
		if app.Status != AppActive && app.Status != AppEvictedCold {
			continue
		}
		if !IsDeveloperApp(app) && !IsPRPreviewApp(app) {
			deployed = append(deployed, app)
		}
	}
	sort.SliceStable(deployed, func(i, j int) bool {
		if !deployed[i].CreatedAt.Equal(deployed[j].CreatedAt) {
			return deployed[i].CreatedAt.Before(deployed[j].CreatedAt)
		}
		return deployed[i].ID < deployed[j].ID
	})
	out := map[string]PlanOverage{}
	if limits.DeployedApps > 0 {
		for _, app := range deployed[min(limits.DeployedApps, len(deployed)):] {
			out[app.ID] = PlanOverage{DeployedApps: len(deployed)}
		}
	}
	for _, app := range apps {
		if (app.Status == AppActive || app.Status == AppEvictedCold) && limits.RAMMB > 0 && app.RAMMB > limits.RAMMB {
			over := out[app.ID]
			over.RAMMB = app.RAMMB
			out[app.ID] = over
		}
	}
	return out
}

// Problem is the wake refusal for an app outside limits: the RAM refusal
// when its RAM is above the plan's, else the deployed-app refusal.
func (o PlanOverage) Problem(limits api.Limits) *api.Problem {
	if o.RAMMB > 0 {
		p := api.ErrPlanLimitRAM(limits, o.RAMMB)
		p.Detail += " Lower the app's RAM (gregale app <slug> --ram) or upgrade to wake it."
		return p
	}
	p := api.ErrPlanLimitApps(limits, o.DeployedApps)
	p.Detail += " Only the oldest deployed apps within the plan wake; delete apps or upgrade."
	return p
}
