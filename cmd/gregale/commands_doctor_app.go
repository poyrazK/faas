package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// This is an observation report, not an active probe or a health guarantee.
// The window bounds evidence age; it does not change platform quotas.
const doctorAppEvidenceWindow = 5 * time.Minute

type doctorApp struct {
	Slug         string `json:"slug"`
	CheckedAt    string `json:"checked_at"`
	DeploymentID string `json:"deployment_id,omitempty"`
}

type doctorAppInputs struct {
	Deployments   api.DeploymentListResponse
	DeploymentErr error
	Instances     []api.InstanceResponse
	InstanceErr   error
	Events        api.ListAuditEventsResponse
	EventErr      error
	Analytics     api.RequestAnalyticsResponse
	AnalyticsErr  error
}

func runDoctorAppCommand(slug string, strict, asJSON bool) int {
	client, err := authedClient()
	if err != nil {
		_ = printErr("Not logged in", err)
		return 3
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		_ = printErr("Could not load app", err)
		return 3
	}
	if app.ID == "" || app.Slug != slug {
		_, _ = fmt.Fprintln(osStderr, "The API response did not identify the requested app.")
		return 3
	}
	now := time.Now().UTC()
	in := loadDoctorAppInputs(ctx, client, app, now)
	report := buildDoctorAppReport(app, in, time.Now().UTC())
	if asJSON {
		if err := writeJSON(report); err != nil {
			return 3
		}
	} else {
		renderDoctorAppHuman(osStdout, report)
	}
	return doctorAppExitCode(report, strict)
}

// All reads are authenticated and bounded. Anonymous lifecycle events still
// pass the API's account/app ownership check. No app URL is requested or woken.
func loadDoctorAppInputs(ctx context.Context, client *Client, app api.AppResponse, now time.Time) doctorAppInputs {
	var in doctorAppInputs
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		in.Deployments, in.DeploymentErr = client.ListAppDeployments(ctx, app.Slug, "", 100)
	}()
	go func() {
		defer wg.Done()
		in.Instances, in.InstanceErr = client.ListInstancesWithHistory(ctx, app.Slug, true)
	}()
	go func() {
		defer wg.Done()
		in.Events, in.EventErr = client.ListAuditEvents(ctx, now.Add(-doctorAppEvidenceWindow).Format(time.RFC3339), "", app.ID, 100, true)
	}()
	go func() {
		defer wg.Done()
		in.Analytics, in.AnalyticsErr = client.GetAppRequestAnalyticsOpts(ctx, app.Slug, api.AppRequestAnalyticsOptions{Since: now.Add(-doctorAppEvidenceWindow).Format(time.RFC3339), Until: now.Format(time.RFC3339)})
	}()
	wg.Wait()
	return in
}

func buildDoctorAppReport(app api.AppResponse, in doctorAppInputs, now time.Time) doctorReport {
	for _, dep := range in.Deployments.Items {
		if dep.AppID != app.ID || dep.ID == "" {
			in.DeploymentErr = errors.New("deployment identity mismatch")
			in.Deployments.Items = nil
			break
		}
	}
	current, candidate := selectInspectDeployments(in.Deployments.Items)
	report := doctorReport{App: &doctorApp{Slug: app.Slug, CheckedAt: now.Format(time.RFC3339)}, Checks: []doctorCheck{}}
	if current != nil {
		report.App.DeploymentID = current.ID
	}
	report.Checks = append(report.Checks, doctorAppDeploymentCheck(app, current, candidate, in))
	report.Checks = append(report.Checks, doctorAppStartupCheck(app, current, candidate, in, now))
	report.Checks = append(report.Checks, doctorAppOOMCheck(app, current, in, now))
	report.Checks = append(report.Checks, doctorAppMemoryCheck(app, current, in, now))
	return report
}

func doctorUnknown(name, reason string) doctorCheck {
	return doctorCheck{Name: name, Status: "unknown", Reason: reason}
}

func doctorAppDeploymentCheck(app api.AppResponse, current, candidate *api.DeploymentResponse, in doctorAppInputs) doctorCheck {
	if in.DeploymentErr != nil {
		return doctorUnknown("deployment", "Deployment history could not be read. Check login, read permissions, and API connectivity.")
	}
	if current == nil {
		if app.DeploymentAvailability == api.AppDeploymentAvailabilityMissing {
			return doctorCheck{Name: "deployment", Status: "error", Code: "no_live_deployment", Hint: "This app has no live deployment.", Fix: "Deploy an application before checking its runtime."}
		}
		return doctorUnknown("deployment", "No deployment evidence was returned.")
	}
	if app.DeploymentAvailability == api.AppDeploymentAvailabilityMissing && current.Status != "failed" {
		return doctorCheck{Name: "deployment", Status: "error", Code: "no_live_deployment", Hint: "This app has no assigned live deployment.", Fix: "Check `gregale apps info " + app.Slug + "` and deploy an application."}
	}
	if candidate != nil {
		return doctorDeploymentFinding("deployment", *candidate, "warn")
	}
	if current.Status == "failed" {
		if app.DeploymentAvailability == api.AppDeploymentAvailabilityLive {
			return doctorUnknown("deployment", "A serving release was not found in the bounded deployment history.")
		}
		return doctorDeploymentFinding("deployment", *current, "error")
	}
	if current.Status != "live" || current.TrafficPercent <= 0 {
		return doctorUnknown("deployment", "The selected deployment is not serving traffic yet.")
	}
	if app.Status != "active" {
		return doctorCheck{Name: "deployment", Status: "warn", Code: "app_not_active", DeploymentID: current.ID, Hint: "The app is not active.", Fix: "Check `gregale apps info " + app.Slug + "` before expecting traffic."}
	}
	return doctorCheck{Name: "deployment", Status: "ok", DeploymentID: current.ID, Hint: "A live deployment is assigned traffic. Runtime checks below use recorded observations."}
}

func doctorDeploymentFinding(name string, dep api.DeploymentResponse, status string) doctorCheck {
	c := doctorCheck{Name: name, Status: status, Code: dep.ErrorCode, DeploymentID: dep.ID, DeploymentCreatedAt: dep.CreatedAt, Hint: "The deployment failed.", Fix: "Run `gregale deploys status " + dep.ID + "` for its recorded explanation."}
	if c.Code == "" {
		c.Code = "deployment_failed"
	}
	// Use the explanation catalog, not arbitrary persisted logs or provider text.
	p := api.NewProblem(0, dep.ErrorCode, "", "")
	p = whycopy.Decorate(p, dep.ErrorCode, nil)
	if p.Hint != "" {
		c.Hint, c.Why, c.Fix = p.Hint, p.Why, p.Fix
	}
	return c
}

func doctorRecent(at string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, at)
	return err == nil && !t.After(now) && !t.Before(now.Add(-doctorAppEvidenceWindow))
}

func doctorAppStartupCheck(app api.AppResponse, current, candidate *api.DeploymentResponse, in doctorAppInputs, now time.Time) doctorCheck {
	if doctorDeploymentUnresolved(app, current, in) {
		return doctorUnknown("startup-timeout", "A deployment must be resolved before evaluating startup evidence.")
	}
	dep, status := current, "error"
	if candidate != nil {
		dep, status = candidate, "warn"
	}
	if dep.Status == "failed" && (dep.ErrorCode == api.CodeAppStartupTimeout || dep.ErrorCode == api.CodeStageReadinessFailed) {
		return doctorDeploymentFinding("startup-timeout", *dep, status)
	}
	if in.EventErr != nil || in.InstanceErr != nil {
		return doctorUnknown("startup-timeout", "Recent lifecycle observations could not be read.")
	}
	var latest *api.AuditEventResponse
	for _, event := range in.Events.Events {
		if (event.Kind != events.WakeReadiness200 && event.Kind != events.WakeBootFailed) || !doctorRecent(event.At, now) {
			continue
		}
		var data struct {
			AppID      string `json:"app_id"`
			InstanceID string `json:"instance_id"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.AppID != app.ID {
			continue
		}
		for _, instance := range in.Instances {
			if instance.ID == data.InstanceID && instance.AppID == app.ID && instance.DeploymentID == current.ID {
				at, _ := time.Parse(time.RFC3339Nano, event.At)
				if latest == nil {
					copy := event
					latest = &copy
				} else if prev, _ := time.Parse(time.RFC3339Nano, latest.At); at.After(prev) || (at.Equal(prev) && event.Kind == events.WakeBootFailed) {
					copy := event
					latest = &copy
				}
			}
		}
	}
	if latest != nil {
		if latest.Kind == events.WakeBootFailed {
			return doctorCheck{Name: "startup-timeout", Status: "error", Code: "startup_failed", DeploymentID: current.ID, ObservedAt: latest.At, Hint: "The most recent observed startup failed. This alone does not establish a timeout.", Fix: "Check `gregale logs " + app.Slug + "` and the configured readiness probe."}
		}
		return doctorCheck{Name: "startup-timeout", Status: "ok", DeploymentID: current.ID, ObservedAt: latest.At, Hint: "A recent readiness probe succeeded for this deployment."}
	}
	return doctorUnknown("startup-timeout", "No recent readiness result could be tied to this deployment. Quiet or parked apps may have no recent wake evidence.")
}

func doctorAppOOMCheck(app api.AppResponse, current *api.DeploymentResponse, in doctorAppInputs, now time.Time) doctorCheck {
	if doctorDeploymentUnresolved(app, current, in) {
		return doctorUnknown("runtime-oom", "A deployment must be resolved before evaluating OOM evidence.")
	}
	if current.Status == "failed" && current.ErrorCode == api.CodeAppRuntimeOOM {
		return doctorDeploymentFinding("runtime-oom", *current, "error")
	}
	if in.EventErr != nil {
		return doctorUnknown("runtime-oom", "Recent lifecycle observations could not be read.")
	}
	for _, event := range in.Events.Events {
		if event.Kind != events.InstanceWorkloadOOMFailed || !doctorRecent(event.At, now) {
			continue
		}
		var data struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.AppID != app.ID || data.DeploymentID != current.ID {
			continue
		}
		c := doctorDeploymentFinding("runtime-oom", api.DeploymentResponse{ID: current.ID, ErrorCode: api.CodeAppRuntimeOOM, CreatedAt: event.At}, "error")
		c.ObservedAt, c.DeploymentCreatedAt = event.At, ""
		return c
	}
	if in.Events.Limit <= 0 || len(in.Events.Events) >= in.Events.Limit || !doctorHasRecentDeploymentEvent(app.ID, current.ID, in, now) {
		return doctorUnknown("runtime-oom", "The recent event window is empty or may be truncated; absence of an OOM event cannot be certified.")
	}
	return doctorCheck{Name: "runtime-oom", Status: "ok", DeploymentID: current.ID, WindowStart: now.Add(-doctorAppEvidenceWindow).Format(time.RFC3339), WindowEnd: now.Format(time.RFC3339), Hint: "No OOM event was recorded for this deployment in the bounded event window. This does not prove complete capture."}
}

func doctorDeploymentUnresolved(app api.AppResponse, current *api.DeploymentResponse, in doctorAppInputs) bool {
	return in.DeploymentErr != nil || current == nil || (app.DeploymentAvailability == api.AppDeploymentAvailabilityMissing && current.Status != "failed") || (app.DeploymentAvailability == api.AppDeploymentAvailabilityLive && (current.Status != "live" || current.TrafficPercent <= 0))
}

func doctorHasRecentDeploymentEvent(appID, deploymentID string, in doctorAppInputs, now time.Time) bool {
	for _, event := range in.Events.Events {
		var data struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
			InstanceID   string `json:"instance_id"`
		}
		if !doctorRecent(event.At, now) || json.Unmarshal(event.Data, &data) != nil || data.AppID != appID {
			continue
		}
		if data.DeploymentID == deploymentID {
			return true
		}
		if in.InstanceErr == nil {
			for _, instance := range in.Instances {
				if instance.ID == data.InstanceID && instance.AppID == appID && instance.DeploymentID == deploymentID {
					return true
				}
			}
		}
	}
	return false
}

func doctorAppMemoryCheck(app api.AppResponse, current *api.DeploymentResponse, in doctorAppInputs, now time.Time) doctorCheck {
	a := in.Analytics
	if in.AnalyticsErr != nil || doctorDeploymentUnresolved(app, current, in) || app.RAMMB <= 0 {
		return doctorUnknown("runtime-memory", "Sampled memory evidence or the deployment memory limit is unavailable. Analytics may require a higher plan or read scope.")
	}
	from, fromErr := time.Parse(time.RFC3339Nano, a.From)
	until, untilErr := time.Parse(time.RFC3339Nano, a.Until)
	if a.Slug != app.Slug || fromErr != nil || untilErr != nil || !doctorRecent(a.Until, now) || !doctorRecent(a.AsOf, now) || !from.Before(until) || until.Sub(from) > doctorAppEvidenceWindow {
		return doctorUnknown("runtime-memory", "The analytics window is missing, stale, or does not identify this app.")
	}
	peak := 0
	missing := false
	for _, route := range a.Routes {
		if route.GuestPeakRSSMaxMB == nil || *route.GuestPeakRSSMaxMB <= 0 {
			missing = true
			continue
		}
		// Route RSS is not split by revision. Accept it only when the API
		// attributes every request in that route to the selected deployment.
		if route.Requests <= 0 || len(route.DeploymentObservations) != 1 || route.OtherDeploymentRequests != 0 || route.DeploymentObservations[0].DeploymentID != current.ID || route.DeploymentObservations[0].Requests != route.Requests {
			return doctorUnknown("runtime-memory", "Sampled memory spans multiple or unidentified deployments.")
		}
		peak = max(peak, *route.GuestPeakRSSMaxMB)
	}
	if peak == 0 {
		return doctorUnknown("runtime-memory", "No supported guest memory samples were captured. Persistent workers and arbitrary HTTP containers may not provide this evidence.")
	}
	c := doctorCheck{Name: "runtime-memory", Status: "ok", DeploymentID: current.ID, WindowStart: a.From, WindowEnd: a.Until, Hint: fmt.Sprintf("Observed process peak: %d MiB; configured app memory: %d MiB. Sampled process RSS does not measure total VM memory.", peak, app.RAMMB)}
	if float64(peak)/float64(app.RAMMB) >= 0.9 {
		c.Status, c.Code = "warn", "sampled_memory_pressure"
		c.Fix = "Review allocations and concurrency. Compare repeated samples before changing the app's memory configuration."
	} else if a.RoutesTruncated || missing {
		return doctorUnknown("runtime-memory", "The route sample is truncated or lacks measurements and may omit a higher memory peak.")
	}
	return c
}

func doctorAppExitCode(report doctorReport, strict bool) int {
	if report.HasErrors() || (strict && report.HasWarnings()) {
		return 1
	}
	for _, check := range report.Checks {
		if check.Status == "unknown" {
			return 3
		}
	}
	return 0
}

func renderDoctorAppHuman(w io.Writer, report doctorReport) {
	_, _ = fmt.Fprintf(w, "gregale doctor — app %s\nChecked at: %s\n", report.App.Slug, report.App.CheckedAt)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(w, "\n  %s — %s", check.Name, check.Status)
		if check.Code != "" {
			_, _ = fmt.Fprintf(w, " (%s)", check.Code)
		}
		_, _ = fmt.Fprintln(w)
		if check.DeploymentID != "" {
			_, _ = fmt.Fprintf(w, "    deployment: %s\n", check.DeploymentID)
		}
		if check.ObservedAt != "" {
			_, _ = fmt.Fprintf(w, "    evidence recorded: %s\n", check.ObservedAt)
		}
		if check.DeploymentCreatedAt != "" {
			_, _ = fmt.Fprintf(w, "    deployment created: %s\n", check.DeploymentCreatedAt)
		}
		if check.WindowStart != "" {
			_, _ = fmt.Fprintf(w, "    window: %s — %s\n", check.WindowStart, check.WindowEnd)
		}
		if check.Reason != "" {
			_, _ = fmt.Fprintf(w, "    reason: %s\n", check.Reason)
		}
		if check.Hint != "" {
			RenderHintRow(w, check.Hint)
		}
		if check.Why != "" {
			RenderWhyRow(w, check.Why)
		}
		if check.Fix != "" {
			RenderFixRow(w, check.Fix)
		}
	}
}
