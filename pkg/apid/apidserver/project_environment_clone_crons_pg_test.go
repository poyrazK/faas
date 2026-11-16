//go:build !no_pg

// adr: 590
package apidserver_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// publish runs the coordinator until the clone is ready, marking its target
// deployments live in between.
func (d fullCloneFixture) publish(operationID string) {
	d.t.Helper()
	current := d.step(operationID)
	if current.Status != state.CloneOperationCopying {
		d.t.Fatalf("capture and copy: %+v", current)
	}
	for _, view := range d.targets(operationID) {
		if err := (*d.f.Store()).MarkDeploymentLiveDark(d.t.Context(), view.TargetDeploymentID); err != nil {
			d.t.Fatal(err)
		}
	}
	for range 3 {
		if current = d.step(operationID); current.Status == state.CloneOperationReady {
			return
		}
	}
	d.t.Fatalf("the clone did not publish: %+v", current)
}

func (d fullCloneFixture) crons(appID, scope string) map[string]state.Cron {
	d.t.Helper()
	all, err := (*d.f.Store()).ListCronsForApp(d.t.Context(), appID)
	if err != nil {
		d.t.Fatal(err)
	}
	out := map[string]state.Cron{}
	for _, c := range all {
		if c.DeploymentScope == scope {
			out[c.Path] = c
		}
	}
	return out
}

// A full clone copies the source's crons into the stage paused; resuming the
// stage's automation enables the ones the source ran and leaves the source's
// own crons alone (ADR-20261104120000 amendment 2026-11-12).
func TestPGFullCloneCopiesCronsPausedAndResumes(t *testing.T) {
	d := newFullCloneFixture(t, false)
	d.open(runtimeconfig.KeyFullEnvironmentClones, fullCloneAdmissionKey)
	ctx := t.Context()
	store := *d.f.Store()
	app := (*d.f.Apps())[0]
	limits := api.MustLimitsFor(d.acct.Plan)
	running, err := store.CreateCronIfUnderQuotaWithOptions(ctx, app.ID, "*/5 * * * *", "/tick", true, limits, state.CronOptions{Timezone: "Europe/Istanbul", SkipIfRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCronIfUnderQuotaWithOptions(ctx, app.ID, "0 3 * * *", "/off", false, limits, state.CronOptions{}); err != nil {
		t.Fatal(err)
	}

	op := d.admitted("stage", "crons")
	d.publish(op.OperationID)

	staged := d.crons(app.ID, "stage")
	tick, off := staged["/tick"], staged["/off"]
	if len(staged) != 2 || tick.Enabled || !tick.ClonePaused || off.Enabled || off.ClonePaused ||
		tick.Schedule != running.Schedule || tick.Timezone != "Europe/Istanbul" || !tick.SkipIfRunning || tick.ID == running.ID {
		t.Fatalf("stage crons = %+v", staged)
	}
	if production := d.crons(app.ID, ""); !production["/tick"].Enabled || production["/tick"].ID != running.ID {
		t.Fatalf("the clone changed production's crons: %+v", production)
	}

	resume := func(environment string) (int, api.ResumeEnvironmentAutomationResponse) {
		req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/"+d.project.Slug+"/environments/"+environment+"/automation/resume", d.project.Slug, nil)
		req.SetPathValue("environment", environment)
		(*d.f.Srv()).ResumeEnvironmentAutomation(rec, req, d.acct)
		var out api.ResumeEnvironmentAutomationResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := resume("stage"); code != http.StatusOK || out.CronsResumed != 1 || len(out.CronIDs) != 1 || out.CronIDs[0] != tick.ID {
		t.Fatalf("resume = %d %+v", code, out)
	}
	staged = d.crons(app.ID, "stage")
	if !staged["/tick"].Enabled || staged["/tick"].ClonePaused || staged["/off"].Enabled {
		t.Fatalf("after resume: %+v", staged)
	}
	if code, out := resume("stage"); code != http.StatusOK || out.CronsResumed != 0 {
		t.Fatalf("a second resume = %d %+v", code, out)
	}
	if code, _ := resume("production"); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
		t.Fatalf("resuming production = %d", code)
	}
	if code, _ := resume("nowhere"); code != http.StatusNotFound {
		t.Fatalf("resuming a missing environment = %d", code)
	}
}

// Copied crons count against the plan's cron limits: admission refuses a
// clone whose copy would pass one, and nothing is created.
func TestPGFullCloneRefusesCronCopyPastPlanLimit(t *testing.T) {
	d := newFullCloneFixture(t, false)
	d.open(runtimeconfig.KeyFullEnvironmentClones, fullCloneAdmissionKey)
	ctx := t.Context()
	store := *d.f.Store()
	app := (*d.f.Apps())[0]
	limits := api.MustLimitsFor(d.acct.Plan)
	for i := range limits.CronLimitPerApp/2 + 1 {
		if _, err := store.CreateCronIfUnderQuotaWithOptions(ctx, app.ID, "*/5 * * * *", "/tick-"+string(rune('a'+i)), false, limits, state.CronOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	rec := d.admit("stage", "over-limit")
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != http.StatusForbidden || problem.Code != api.CodePlanCronQuota {
		t.Fatalf("admission = %d %s, %v", rec.Code, rec.Body.String(), err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, d.acct.ID, d.project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a refused clone created its target: %v", err)
	}
}
