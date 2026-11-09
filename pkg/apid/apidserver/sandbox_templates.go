package apidserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/sandboxes"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// sandboxTemplateApps wires ADR-20261027090013's template build apps: an
// internal app in the job execution mode, so a build ends at a verified
// rootfs and never boots, counted in SandboxTemplatesMax instead of
// DeployedApps.
func (s *server) sandboxTemplateApps() sandboxes.TemplateApps {
	return sandboxes.TemplateApps{Create: s.createSandboxTemplateApp, Delete: s.deleteSandboxTemplateApp}
}

func (s *server) createSandboxTemplateApp(ctx context.Context, r *http.Request, acct state.Account) (string, *api.Problem) {
	limits := api.MustLimitsFor(acct.Plan)
	for attempt := 0; ; attempt++ {
		slug, err := sandboxTemplateSlug()
		if err != nil {
			return "", api.ErrCapacity("could not name the template's build app")
		}
		app, prob := s.buildApp(acct, api.CreateAppRequest{Slug: slug, Visibility: string(api.AppVisibilityInternal)}, limits)
		if prob != nil {
			return "", prob
		}
		app.SandboxTemplate = true
		app.WorkloadClass = state.WorkloadClassJob
		app.Manifest.ExecutionMode = api.ExecutionModeJob
		app.NodeID = ""
		created, err := s.createAppIfUnderQuotaWithActivity(ctx, r, acct, app, limits)
		var qe *state.QuotaError
		switch {
		case err == nil:
			s.emitAppCreated(ctx, created)
			return created.ID, nil
		case errors.As(err, &qe) && qe.Kind == state.QuotaErrorKindSandboxTemplates:
			return "", api.ErrAgentLimit(acct.Plan, "SandboxTemplatesMax", qe.Limit, qe.Observed+1)
		case errors.As(err, &qe):
			return "", appaccess.QuotaProblem(qe, limits)
		case errors.Is(err, state.ErrConflict) && attempt == 0:
			continue
		case state.ServiceCapacityProblem(err) != nil:
			return "", state.ServiceCapacityProblem(err)
		default:
			s.log.Warn("apid: create sandbox template app", "account_id", acct.ID, "err", err)
			return "", api.ErrCapacity("could not create the template's build app")
		}
	}
}

// deleteSandboxTemplateApp schedules the build app's deletion like an app
// delete; its builds' artifacts follow the app's grace sweep.
func (s *server) deleteSandboxTemplateApp(ctx context.Context, r *http.Request, acct state.Account, appID string) *api.Problem {
	app, err := s.store.AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) || err == nil && app.Status == state.AppDeleted {
		return nil
	}
	if err != nil || !app.SandboxTemplate || app.AccountID != acct.ID {
		if err == nil {
			err = fmt.Errorf("app %s is not this account's template build app", appID)
		}
		s.log.Warn("apid: delete sandbox template app", "app_id", appID, "err", err)
		return api.ErrCapacity("could not delete the template's build app")
	}
	if _, err := s.scheduleAppDeletionWithActivity(ctx, r, acct, app, time.Now().UTC().Add(state.AppDeleteGraceDuration())); err != nil {
		s.log.Warn("apid: delete sandbox template app", "app_id", appID, "err", err)
		return api.ErrCapacity("could not delete the template's build app")
	}
	_ = s.notif.Notify(ctx, db.NotifyAppDelete, fmt.Sprintf(`{"slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	return nil
}

// sandboxTemplateSlug names a build app: sbt- and 12 random hex digits.
func sandboxTemplateSlug() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sbt-" + hex.EncodeToString(b[:]), nil
}
