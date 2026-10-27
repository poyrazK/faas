package apidserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// planDowngradeProblem is the pre-downgrade quota check of spec §10: a
// downgrade is refused, with the list of changes that would make it fit,
// while the account's or organization's apps exceed the target plan's
// deployed-app count or per-app RAM (Hunt 17 H17-4). Without it a downgrade
// was scheduled with the provider and every app kept running above the new
// plan, because limits are checked only when an app is written.
func (s *server) planDowngradeProblem(ctx context.Context, subject state.Account, target api.Plan) *api.Problem {
	limits, ok := api.LimitsFor(target)
	if !ok {
		return nil
	}
	apps, err := s.planSubjectApps(ctx, subject)
	if err != nil {
		s.log.Error("plan downgrade: list apps", "account", subject.ID, "err", err)
		return api.ErrCapacity("could not check the plan's limits; try again")
	}
	deployed := 0
	var ramTasks []string
	largest := 0
	for _, app := range apps {
		if app.Status != state.AppActive && app.Status != state.AppEvictedCold {
			continue
		}
		if !state.IsDeveloperApp(app) && !state.IsPRPreviewApp(app) {
			deployed++
		}
		if app.RAMMB > limits.RAMMB {
			ramTasks = append(ramTasks, fmt.Sprintf("reduce %s's RAM from %d MB to %d MB or less", app.Slug, app.RAMMB, limits.RAMMB))
			if app.RAMMB > largest {
				largest = app.RAMMB
			}
		}
	}
	var tasks []string
	if deployed > limits.DeployedApps {
		tasks = append(tasks, fmt.Sprintf("delete %d app(s): the %s plan allows %d deployed app(s) and you have %d", deployed-limits.DeployedApps, target, limits.DeployedApps, deployed))
	}
	tasks = append(tasks, ramTasks...)
	if len(tasks) == 0 {
		return nil
	}
	var problem *api.Problem
	if deployed > limits.DeployedApps {
		problem = api.ErrPlanLimitApps(limits, deployed)
	} else {
		problem = api.ErrPlanLimitRAM(limits, largest)
	}
	problem.Detail = fmt.Sprintf("the %s plan does not fit your apps yet; before downgrading: %s", target, strings.Join(tasks, "; "))
	return problem
}

// planSubjectApps is the apps a plan governs: an organization's apps, or an
// account's personal apps (company apps follow their organization's plan).
func (s *server) planSubjectApps(ctx context.Context, subject state.Account) ([]state.App, error) {
	if subject.ResourceOrgID != "" {
		lister, ok := s.store.(state.OrgAppLister)
		if !ok {
			return nil, errors.New("store cannot list organization apps")
		}
		return lister.ListAppsByOrg(ctx, subject.ResourceOrgID)
	}
	apps, err := s.store.ListApps(ctx, subject.ID)
	if err != nil {
		return nil, err
	}
	personalOrg := ""
	if org, err := s.store.OrgByPersonalAccount(ctx, subject.ID); err == nil {
		personalOrg = org.ID
	} else if !errors.Is(err, state.ErrNotFound) {
		return nil, err
	}
	personal := apps[:0]
	for _, app := range apps {
		if app.OrgID == "" || app.OrgID == personalOrg {
			personal = append(personal, app)
		}
	}
	return personal, nil
}
