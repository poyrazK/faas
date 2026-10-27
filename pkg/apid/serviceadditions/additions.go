// Package serviceadditions owns read-only source membership planning and the
// durable reservation seam used by atomic project deployments.
package serviceadditions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/trafficproblem"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func Plan(ctx context.Context, store any, account state.Account, project state.Project, safe *api.ProjectSafeRelease, apps []state.App, scan reposcan.Result) ([]state.App, error) {
	dormant := map[string]bool{}
	reader, available := store.(state.ProjectReleaseAdditionsStore)
	if available {
		var err error
		dormant, err = reader.DormantProjectReleaseApps(ctx, account.ID, project.ID)
		if err != nil {
			return nil, err
		}
	}
	if !safe.AddServices {
		var serving []state.App
		for _, app := range apps {
			if !dormant[app.ID] {
				serving = append(serving, app)
			}
		}
		return reconcile.ProjectSafeSourceApps(serving, scan, safe.ReleaseCommand != nil, safe.Worker != nil)
	}
	if !available {
		return nil, fmt.Errorf("service addition reservations are unavailable")
	}
	graphs, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		return nil, fmt.Errorf("service addition baseline is unavailable")
	}
	baseline, err := graphs.ActiveProjectReleaseSet(ctx, account.ID, project.ID, "production")
	if err != nil || baseline.ID != safe.PreviousReleaseID {
		return nil, fmt.Errorf("service addition baseline changed")
	}
	serving := map[string]bool{}
	for _, app := range apps {
		if !dormant[app.ID] && app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			serving[app.ID] = true
		}
	}
	if len(serving) != len(baseline.Members) {
		return nil, fmt.Errorf("project inventory differs from the serving baseline; reconcile it before adding services")
	}
	for _, member := range baseline.Members {
		if !serving[member.AppID] {
			return nil, fmt.Errorf("serving baseline membership changed")
		}
	}
	selected, drafts, additions, err := reconcile.ProjectSourceAdditions(project, account.Plan, safe.TTLSeconds, apps, dormant, scan)
	if err != nil {
		return nil, err
	}
	if len(additions) == 0 {
		return nil, fmt.Errorf("--add-services requires at least one new HTTP service")
	}
	safe.Additions = additions
	safe.Deadline = time.Now().UTC().Add(api.ProjectSafeReleaseGracePeriod + time.Duration((len(selected)+len(drafts))*api.BuildTimeoutSeconds)*time.Second)
	return append(selected, drafts...), nil
}

func Stage(ctx context.Context, store state.ProjectReleaseAdditionsStore, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease, selected []state.App) ([]state.App, error) {
	var drafts []state.App
	for _, app := range selected {
		for _, addition := range safe.Additions {
			if addition.Slug == app.Slug {
				drafts = append(drafts, app)
			}
		}
	}
	reserved, err := store.StageProjectReleaseAdditions(ctx, accountID, attemptID, projectID, safe, drafts)
	if err != nil {
		return nil, err
	}
	for _, app := range reserved {
		for i := range selected {
			if selected[i].Slug == app.Slug {
				selected[i] = app
			}
		}
		for i := range safe.Additions {
			if safe.Additions[i].Slug == app.Slug {
				safe.Additions[i].AppID = app.ID
			}
		}
	}
	return selected, nil
}

func Partition(apps []state.App, safe *api.ProjectSafeRelease) (added, serving []state.App) {
	ids := map[string]bool{}
	for _, addition := range safe.Additions {
		ids[addition.AppID] = true
	}
	for _, app := range apps {
		if ids[app.ID] {
			added = append(added, app)
		} else {
			serving = append(serving, app)
		}
	}
	return added, serving
}

type Auditor interface {
	Emit(context.Context, string, *string, map[string]any)
}

func AuditReservations(ctx context.Context, auditor Auditor, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) {
	if auditor == nil {
		return
	}
	for _, addition := range safe.Additions {
		auditor.Emit(ctx, "project.workload.reserved", &accountID, map[string]any{
			"project_id": projectID, "app_id": addition.AppID, "workload_name": addition.Slug,
			"root_dir": addition.RootDir, "attempt_id": attemptID, "baseline_release_id": safe.PreviousReleaseID,
		})
	}
}

func ValidateHeader(header string, values int, mode string, configuration bool, workers bool, progressive bool) error {
	if header == "" {
		return nil
	}
	if header != "required" || values != 1 || mode != "atomic" && mode != "atomic-verify" || configuration || workers || progressive {
		return fmt.Errorf("service additions require atomic HTTP source deployment without configuration, secret, migration, worker or progressive changes")
	}
	return nil
}

func ReservationProblem(err error, plan api.Plan) *api.Problem {
	var quota *state.QuotaError
	if errors.As(err, &quota) {
		return api.ErrPlanLimitApps(api.MustLimitsFor(plan), quota.Observed)
	}
	// A new service's hostname can expose an oversized policy (ADR-570).
	if problem := trafficproblem.Policy(err, nil); problem != nil {
		return problem
	}
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Service addition reservation changed", "Project membership or reserved service settings changed; scan again.")
}

func ValidatePlanToken(token, identity string) error {
	if token == "" {
		return nil
	}
	var plan struct {
		SafeReleaseID string `json:"safe_release_id"`
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return fmt.Errorf("invalid safe release plan")
	}
	if err := json.Unmarshal(data, &plan); err != nil || plan.SafeReleaseID != identity {
		return fmt.Errorf("safe release baseline or membership changed after scan")
	}
	return nil
}
