package apidserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// planDowngradeProblem is the pre-downgrade quota check of spec §10: a
// downgrade is refused, with the list of changes that would make it fit,
// while the account's or organization's apps exceed the target plan's
// deployed-app count or per-app RAM (Hunt 17 H17-4). Without it a downgrade
// was scheduled with the provider and every app kept running above the new
// plan, because limits are checked only when an app is written. Managed
// PostgreSQL databases are checked the same way: their count and service
// class against the target plan (ADR-20261031091607).
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
	databaseTasks, databases, databasesMax, err := s.planDowngradeDatabaseTasks(ctx, subject, target)
	if err != nil {
		s.log.Error("plan downgrade: list managed postgres databases", "account", subject.ID, "err", err)
		return api.ErrCapacity("could not check the plan's limits; try again")
	}
	tasks = append(tasks, databaseTasks...)
	if len(tasks) == 0 {
		return nil
	}
	var problem *api.Problem
	switch {
	case deployed > limits.DeployedApps:
		problem = api.ErrPlanLimitApps(limits, deployed)
	case len(ramTasks) > 0:
		problem = api.ErrPlanLimitRAM(limits, largest)
	default:
		problem = api.NewProblem(403, "managed_postgres_quota_exceeded", "Managed PostgreSQL quota exceeded", "").
			WithLimit(int64(databasesMax), int64(databases)).WithDocs("https://gregale.dev/docs/plans")
	}
	problem.Detail = fmt.Sprintf("the %s plan does not fit your apps and databases yet; before downgrading: %s", target, strings.Join(tasks, "; "))
	return problem
}

// planDowngradeDatabaseTasks lists what the subject's managed PostgreSQL
// databases need before the target plan fits them: fewer databases, or a
// service class the plan includes. A database whose delete is scheduled no
// longer counts. It reports the live count and the plan's database limit.
func (s *server) planDowngradeDatabaseTasks(ctx context.Context, subject state.Account, target api.Plan) ([]string, int, int, error) {
	if s.managedPostgres == nil {
		return nil, 0, 0, nil
	}
	limits, _ := api.ManagedPostgresLimitsFor(target)
	if subject.ResourceOrgID != "" {
		ctx = managedpostgres.WithOwnership(ctx, subject.ResourceOrgID, subject.ID)
	}
	databases, err := s.managedPostgres.List(ctx, subject.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	var tasks []string
	live := 0
	for _, database := range databases {
		if database.State == managedpostgres.StateDeleting || database.State == managedpostgres.StateDeleted {
			continue
		}
		live++
		if limits.DatabasesMax > 0 && !managedPostgresPlanAllows(limits, database.Spec) {
			tasks = append(tasks, fmt.Sprintf("resize database %s from the %s class to one the %s plan includes", database.Name, database.Spec.Class, target))
		}
	}
	if live > limits.DatabasesMax {
		tasks = append([]string{fmt.Sprintf("delete %d managed PostgreSQL database(s): the %s plan allows %d and you have %d", live-limits.DatabasesMax, target, limits.DatabasesMax, live)}, tasks...)
	}
	return tasks, live, limits.DatabasesMax, nil
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
