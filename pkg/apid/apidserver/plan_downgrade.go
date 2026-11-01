package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/postgresaccess"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// planDowngradeProblem is the pre-downgrade quota check of spec §10: a
// downgrade is refused, with the list of changes that would make it fit,
// while the account's or organization's apps exceed the target plan's
// deployed-app count or per-app RAM (Hunt 17 H17-4). Without it a downgrade
// was scheduled with the provider and every app kept running above the new
// plan, because limits are checked only when an app is written. Managed
// PostgreSQL databases are checked the same way: their count and each spec
// (bug hunt 33).
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
	databaseTasks, databases, err := s.managedPostgresDowngradeTasks(ctx, subject, target)
	if err != nil {
		s.log.Error("plan downgrade: list managed databases", "account", subject.ID, "err", err)
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
	case largest > 0:
		problem = api.ErrPlanLimitRAM(limits, largest)
	default:
		databaseLimits, _ := api.ManagedPostgresLimitsFor(target)
		problem = api.NewProblem(http.StatusForbidden, "managed_postgres_not_in_plan", "Databases over plan limit",
			fmt.Sprintf("%s plan allows %d managed database(s); you have %d.", target, databaseLimits.DatabasesMax, databases)).
			WithLimit(int64(databaseLimits.DatabasesMax), int64(databases)).
			WithDocs("https://gregale.dev/docs/plans")
	}
	problem.Detail = fmt.Sprintf("the %s plan does not fit your apps yet; before downgrading: %s", target, strings.Join(tasks, "; "))
	return problem
}

// managedPostgresDowngradeTasks lists what the subject's managed PostgreSQL
// databases need before the target plan fits: fewer databases, and each
// database's service class, storage, restore window and always-on compute
// within the plan. It returns the number of live databases.
func (s *server) managedPostgresDowngradeTasks(ctx context.Context, subject state.Account, target api.Plan) ([]string, int, error) {
	if s.managedPostgres == nil {
		return nil, 0, nil
	}
	limits, _ := api.ManagedPostgresLimitsFor(target)
	var databases []managedpostgres.Database
	var err error
	if subject.ResourceOrgID != "" {
		databases, err = s.managedPostgres.List(managedpostgres.WithOwnership(ctx, subject.ResourceOrgID, subject.ResourceOrgID), subject.ResourceOrgID)
	} else {
		scoped, scopeErr := postgresaccess.PersonalContext(ctx, s.store, subject.ID)
		if scopeErr != nil {
			return nil, 0, scopeErr
		}
		databases, err = s.managedPostgres.List(scoped, subject.ID)
	}
	if err != nil {
		return nil, 0, err
	}
	live := 0
	var specTasks []string
	for _, database := range databases {
		if database.State == managedpostgres.StateDeleting || database.State == managedpostgres.StateDeleted {
			continue
		}
		live++
		if limits.DatabasesMax > 0 && !managedPostgresSpecWithinPlan(limits, database.Spec) {
			specTasks = append(specTasks, fmt.Sprintf("resize database %s to fit the %s plan's service class, storage and restore window", database.Name, target))
		}
	}
	var tasks []string
	if live > limits.DatabasesMax {
		tasks = append(tasks, fmt.Sprintf("delete %d managed database(s): the %s plan allows %d and you have %d", live-limits.DatabasesMax, target, limits.DatabasesMax, live))
	}
	return append(tasks, specTasks...), live, nil
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
