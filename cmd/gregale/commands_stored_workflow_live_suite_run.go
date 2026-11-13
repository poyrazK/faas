package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowlive"
)

func liveSuiteCaseTarget(p storedWorkflowLiveSuitePlan, name string) api.StartStoredWorkflowLiveVerificationRequest {
	target := p.Target
	target.CaseName = name
	return target
}

func checkStoredWorkflowLiveSuiteTarget(ctx context.Context, c *api.Client, p storedWorkflowLiveSuitePlan) (string, int) {
	requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	app, err := c.GetApp(requestCtx, p.App)
	cancel()
	if err != nil {
		return "Could not read the suite app identity.", liveSuiteErrorCode(ctx, err)
	}
	if app.ID != p.AppID || app.Slug != p.App {
		return "The suite app identity changed.", 5
	}
	requestCtx, cancel = context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	draft, err := c.GetStoredWorkflow(requestCtx, p.App, p.Name)
	cancel()
	if err != nil {
		return "Could not read the suite stored workflow target.", liveSuiteErrorCode(ctx, err)
	}
	if draft.Name != p.Name || draft.Version != p.Target.ExpectedVersion || draft.PublishedVersion != p.Target.ExpectedPublishedVersion || draft.Published == nil || draft.TestSuite == nil {
		return "The saved suite target changed. Review a new suite explicitly.", 5
	}
	for _, name := range p.Cases {
		found := false
		for _, saved := range draft.TestSuite.Cases {
			if saved.Name == name && saved.Live != nil && storedworkflowlive.Validate(*draft.Published, *saved.Live) == nil {
				found = true
				break
			}
		}
		if !found {
			return "A selected case is missing or has invalid live expectations for the published definition.", 1
		}
	}
	requestCtx, cancel = context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	target, err := c.ListStoredWorkflowLiveVerifications(requestCtx, p.App, p.Name)
	cancel()
	if err != nil {
		return "Could not read the suite deployment target.", liveSuiteErrorCode(ctx, err)
	}
	actual, aerr := uuid.Parse(target.DeploymentID)
	expected, _ := uuid.Parse(p.Target.ExpectedDeploymentID)
	if aerr != nil || actual != expected || target.PublishedVersion != p.Target.ExpectedPublishedVersion {
		return "The published revision or deployment changed. Review a new suite explicitly.", 5
	}
	return "", 0
}

func liveSuiteErrorCode(ctx context.Context, err error) int {
	if errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	if ctx.Err() != nil {
		return 124
	}
	var failure *api.APIError
	if errors.As(err, &failure) {
		return exitCodeForStatus(failure.Problem.Status)
	}
	return 3
}

func liveSuiteHTTPStatus(err error) int {
	var failure *api.APIError
	if errors.As(err, &failure) {
		return failure.Problem.Status
	}
	return 503
}

func stopStoredWorkflowLiveSuiteCase(path string, j *storedWorkflowLiveSuiteJournal, index, code int) int {
	if j.Cases[index].AttemptedAt != nil {
		j.Cases[index].Outcome = "error"
		if code == 124 {
			j.Cases[index].Outcome = "timed_out"
		}
		if code == 130 {
			j.Cases[index].Outcome = "interrupted"
		}
		if err := saveStoredWorkflowLiveSuite(path, j, false); err != nil {
			return 3
		}
	}
	return code
}

func executeStoredWorkflowLiveSuite(ctx context.Context, c *api.Client, o storedWorkflowLiveSuiteOptions, j *storedWorkflowLiveSuiteJournal) int {
	if message, code := checkStoredWorkflowLiveSuiteTarget(ctx, c, j.Plan); code != 0 {
		return renderStoredWorkflowLiveSuite(o.path, *j, "blocked", message, code)
	}
	if err := syncStoredWorkflowLiveSuite(ctx, c, *j); err != nil {
		return renderStoredWorkflowLiveSuite(o.path, *j, "blocked", "Could not retain shared suite history. No further cases were admitted; keep the journal and resume explicitly.", liveSuiteErrorCode(ctx, err))
	}
	for index := range j.Cases {
		if ctx.Err() != nil {
			return renderStoredWorkflowLiveSuite(o.path, *j, "stopped", "Suite observation stopped before the next case.", liveSuiteErrorCode(ctx, ctx.Err()))
		}
		view, err := admitOrReadStoredWorkflowLiveSuiteCase(ctx, c, o.path, j, index)
		if err != nil {
			code := stopStoredWorkflowLiveSuiteCase(o.path, j, index, liveSuiteErrorCode(ctx, err))
			return renderStoredWorkflowLiveSuite(o.path, *j, "stopped", "Suite stopped while admitting or reading a case. Keep the journal and resume explicitly.", code)
		}
		if code := observeStoredWorkflowLiveSuiteCase(ctx, c, o, j, index, view, true); code != 0 {
			if code != 1 {
				code = stopStoredWorkflowLiveSuiteCase(o.path, j, index, code)
			}
			return renderStoredWorkflowLiveSuite(o.path, *j, "stopped", "Suite stopped at the first case without a current passing verdict.", code)
		}
	}
	// Recheck every case before declaring the combined result current. External
	// continuations or target changes can invalidate an earlier passing report.
	for index := range j.Cases {
		requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
		view, err := c.GetStoredWorkflowLiveVerification(requestCtx, j.Plan.App, j.Plan.Name, j.Cases[index].RunID)
		cancel()
		if err != nil {
			code := stopStoredWorkflowLiveSuiteCase(o.path, j, index, liveSuiteErrorCode(ctx, err))
			return renderStoredWorkflowLiveSuite(o.path, *j, "stopped", "The final suite refresh failed; stored observations remain historical.", code)
		}
		if code := observeStoredWorkflowLiveSuiteCase(ctx, c, o, j, index, view, false); code != 0 {
			if code != 1 {
				code = stopStoredWorkflowLiveSuiteCase(o.path, j, index, code)
			}
			return renderStoredWorkflowLiveSuite(o.path, *j, "stopped", "A case changed during the final suite refresh.", code)
		}
	}
	if message, code := checkStoredWorkflowLiveSuiteTarget(ctx, c, j.Plan); code != 0 {
		return renderStoredWorkflowLiveSuite(o.path, *j, "blocked", message, code)
	}
	if ctx.Err() != nil {
		return renderStoredWorkflowLiveSuite(o.path, *j, "stopped", "The suite deadline expired during final validation.", liveSuiteErrorCode(ctx, ctx.Err()))
	}
	return renderStoredWorkflowLiveSuite(o.path, *j, "passed", "All selected live cases passed for the frozen suite target.", 0)
}

func admitOrReadStoredWorkflowLiveSuiteCase(ctx context.Context, c *api.Client, path string, j *storedWorkflowLiveSuiteJournal, index int) (api.StoredWorkflowLiveVerification, error) {
	entry := &j.Cases[index]
	var view api.StoredWorkflowLiveVerification
	if ctx.Err() != nil {
		return view, ctx.Err()
	}
	requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
	defer cancel()
	if entry.RunID != "" {
		return c.GetStoredWorkflowLiveVerification(requestCtx, j.Plan.App, j.Plan.Name, entry.RunID)
	}
	if entry.AttemptedAt == nil {
		now := time.Now().UTC()
		entry.AttemptedAt, entry.Outcome = &now, "starting"
		if err := saveStoredWorkflowLiveSuite(path, j, false); err != nil {
			return view, err
		}
	} else if time.Since(*entry.AttemptedAt) >= api.StoredWorkflowLiveSuiteUncertainReplayMaxAge {
		return view, errors.New("uncertain suite admission is too old to replay; inspect the original run")
	}
	if ctx.Err() != nil {
		return view, ctx.Err()
	}
	var err error
	view, err = c.StartStoredWorkflowLiveVerification(requestCtx, j.Plan.App, j.Plan.Name, liveSuiteCaseTarget(j.Plan, entry.Name), entry.Key)
	if err != nil {
		return view, err
	}
	if !validStoredWorkflowLiveSuiteView(j.Plan, *entry, view) {
		return api.StoredWorkflowLiveVerification{}, errors.New("invalid suite admission response")
	}
	// Keep the accepted run in the returned report even if disk persistence fails.
	entry.RunID, entry.Verification, entry.Outcome = view.RunID, &view, "pending"
	if err := saveStoredWorkflowLiveSuite(path, j, false); err != nil {
		return view, err
	}
	return view, syncStoredWorkflowLiveSuite(ctx, c, *j)
}

func validStoredWorkflowLiveSuiteView(p storedWorkflowLiveSuitePlan, entry storedWorkflowLiveSuiteCase, view api.StoredWorkflowLiveVerification) bool {
	if view.Report != nil {
		raw, err := json.Marshal(view.Report)
		if err != nil || int64(len(raw)) > api.StoredWorkflowLiveReportMaxBytes {
			return false
		}
	}
	return validStoredWorkflowLiveView(view, entry.RunID) && storedWorkflowLiveStartMatches(storedWorkflowLiveOptions{verb: "start", start: liveSuiteCaseTarget(p, entry.Name)}, view)
}

func observeStoredWorkflowLiveSuiteCase(ctx context.Context, c *api.Client, o storedWorkflowLiveSuiteOptions, j *storedWorkflowLiveSuiteJournal, index int, view api.StoredWorkflowLiveVerification, wait bool) int {
	for ctx.Err() == nil {
		entry := &j.Cases[index]
		if !validStoredWorkflowLiveSuiteView(j.Plan, *entry, view) {
			return 3
		}
		if !view.Outdated && terminalStoredWorkflowLive(view.RunStatus) && !view.ReportCurrent {
			checked, err := requestStoredWorkflowLiveCheck(ctx, c, storedWorkflowLiveOptions{app: j.Plan.App, name: j.Plan.Name}, view.RunID)
			if err != nil {
				return liveSuiteErrorCode(ctx, err)
			}
			if !validStoredWorkflowLiveSuiteView(j.Plan, *entry, checked) {
				return 3
			}
			view = checked
		}
		if ctx.Err() != nil {
			return liveSuiteErrorCode(ctx, ctx.Err())
		}
		outcome, _, code := storedWorkflowLiveVerdict(view)
		snapshot, now := view, time.Now().UTC()
		entry.Verification, entry.Outcome, entry.ObservedAt = &snapshot, outcome, &now
		if err := saveStoredWorkflowLiveSuite(o.path, j, false); err != nil {
			return 3
		}
		if !wait || outcome != "pending" {
			return code
		}
		if !jsonOutput {
			if _, err := fmt.Fprintf(osStderr, "Live suite %s: %s (%s)\n", entry.Name, view.RunID, view.RunStatus); err != nil {
				return 3
			}
		}
		timer := time.NewTimer(o.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return liveSuiteErrorCode(ctx, ctx.Err())
		case <-timer.C:
		}
		requestCtx, cancel := context.WithTimeout(ctx, api.StoredWorkflowLiveProgressRequestTimeout)
		next, err := c.GetStoredWorkflowLiveVerification(requestCtx, j.Plan.App, j.Plan.Name, view.RunID)
		cancel()
		if err != nil {
			return liveSuiteErrorCode(ctx, err)
		}
		view = next
	}
	return liveSuiteErrorCode(ctx, ctx.Err())
}

type storedWorkflowLiveSuiteCaseResult struct {
	Name         string                              `json:"name"`
	RunID        string                              `json:"run_id,omitempty"`
	Outcome      string                              `json:"outcome"`
	Verification *api.StoredWorkflowLiveVerification `json:"verification,omitempty"`
	ObservedAt   *time.Time                          `json:"observed_at,omitempty"`
}

func renderStoredWorkflowLiveSuite(path string, j storedWorkflowLiveSuiteJournal, outcome, message string, code int) int {
	if code == 124 {
		outcome = "timed_out"
	}
	if code == 130 {
		outcome = "interrupted"
	}
	result := struct {
		SchemaVersion    int                                 `json:"schema_version"`
		App              string                              `json:"app"`
		Name             string                              `json:"name"`
		Outcome          string                              `json:"outcome"`
		ExitCode         int                                 `json:"exit_code"`
		Message          string                              `json:"message"`
		ObservedAt       time.Time                           `json:"observed_at"`
		PublishedVersion int64                               `json:"published_version"`
		DeploymentID     string                              `json:"deployment_id"`
		SharedSuiteID    string                              `json:"shared_suite_id,omitempty"`
		Summary          map[string]int                      `json:"summary"`
		Cases            []storedWorkflowLiveSuiteCaseResult `json:"cases"`
		Resume           string                              `json:"resume"`
	}{SchemaVersion: 1, App: j.Plan.App, Name: j.Plan.Name, Outcome: outcome, ExitCode: code, Message: message, ObservedAt: time.Now().UTC(), PublishedVersion: j.Plan.Target.ExpectedPublishedVersion, DeploymentID: j.Plan.Target.ExpectedDeploymentID, Summary: map[string]int{"selected": len(j.Cases), "passed": 0, "failed": 0, "pending": 0, "not_started": 0, "unresolved": 0}, Cases: []storedWorkflowLiveSuiteCaseResult{}, Resume: recoveryCLI() + " workflow stored live run --resume " + recoveryArgument(path) + " --confirm-real-actions"}
	if j.Plan.Shared {
		result.SharedSuiteID = sharedStoredWorkflowLiveSuiteID(j)
	}
	for _, entry := range j.Cases {
		result.Cases = append(result.Cases, storedWorkflowLiveSuiteCaseResult{entry.Name, entry.RunID, entry.Outcome, entry.Verification, entry.ObservedAt})
		category := entry.Outcome
		if entry.AttemptedAt == nil {
			category = "not_started"
		} else if category != "passed" && category != "failed" && category != "pending" {
			category = "unresolved"
		}
		result.Summary[category]++
	}
	var err error
	if jsonOutput {
		err = writeJSON(result)
	} else {
		_, err = fmt.Fprintf(osStdout, "%s\n", message)
		for _, entry := range result.Cases {
			if err != nil {
				break
			}
			_, err = fmt.Fprintf(osStdout, "  %s: %s %s\n", entry.Name, entry.Outcome, entry.RunID)
		}
		if err == nil {
			if result.SharedSuiteID != "" {
				_, err = fmt.Fprintf(osStdout, "Shared suite: %s\n", result.SharedSuiteID)
			}
		}
		if err == nil {
			_, err = fmt.Fprintf(osStdout, "Resume suite: %s\n", result.Resume)
		}
	}
	if err != nil {
		return printErr("Could not write live suite report", err)
	}
	return code
}
