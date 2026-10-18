package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationlive"
)

type AutomationLiveStore interface {
	StartAutomationLiveVerification(context.Context, string, string, string, api.StartAutomationLiveVerificationRequest, bool) (api.AutomationLiveVerification, bool, error)
	ListAutomationLiveVerifications(context.Context, string, string) (api.ListAutomationLiveVerificationsResponse, error)
	GetAutomationLiveVerification(context.Context, string, string, string) (api.AutomationLiveVerification, error)
	CheckAutomationLiveVerification(context.Context, string, string, string) (api.AutomationLiveVerification, error)
}

type automationLiveRecord struct {
	AppID, Name, Key string
	Fingerprint      []byte
	Test             json.RawMessage
	View             api.AutomationLiveVerification
}

func validateLiveStart(req *api.StartAutomationLiveVerificationRequest, key string) ([]byte, error) {
	if !req.ConfirmRealActions {
		return nil, api.ErrValidation("Confirm that live verification may perform real actions in your app and connected services.")
	}
	id, err := uuid.Parse(req.ExpectedDeploymentID)
	if err != nil || id == uuid.Nil || req.ExpectedVersion <= 0 || req.ExpectedPublishedVersion <= 0 || req.ExpectedPublishedVersion > req.ExpectedVersion {
		return nil, ErrAutomationVersionConflict
	}
	if key == "" || strings.TrimSpace(key) != key || len(key) > WorkflowRunIdempotencyKeyMaxBytes || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return nil, api.ErrValidation("A valid Idempotency-Key is required for live verification.")
	}
	if req.CaseName == "" || strings.TrimSpace(req.CaseName) != req.CaseName || len(req.CaseName) > api.AutomationTestCaseNameMaxBytes || strings.IndexFunc(req.CaseName, unicode.IsControl) >= 0 {
		return nil, ErrAutomationInvalid
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	return hash[:], nil
}

func liveCase(record Automation, name string) (api.WorkflowSpec, api.AutomationTestCase, error) {
	var def api.WorkflowSpec
	var suite api.AutomationTestSuite
	if len(record.Published) == 0 || json.Unmarshal(record.Published, &def) != nil || json.Unmarshal(record.TestSuite, &suite) != nil {
		return def, api.AutomationTestCase{}, ErrAutomationInvalid
	}
	for _, c := range suite.Cases {
		if c.Name == name {
			return def, c, nil
		}
	}
	return def, api.AutomationTestCase{}, ErrAutomationInvalid
}

func liveCaseHash(def api.WorkflowSpec, c api.AutomationTestCase) (string, error) {
	return api.AutomationTestInputsHash(def, api.AutomationTestSuite{Cases: []api.AutomationTestCase{c}})
}

func prepareLiveVerification(appID, name, key, deploymentID string, record Automation, req api.StartAutomationLiveVerificationRequest, fingerprint []byte) (*WorkflowRun, automationLiveRecord, error) {
	if record.Version != req.ExpectedVersion || record.PublishedVersion != req.ExpectedPublishedVersion || !sameMemUUID(deploymentID, req.ExpectedDeploymentID) {
		return nil, automationLiveRecord{}, ErrAutomationVersionConflict
	}
	def, c, err := liveCase(record, req.CaseName)
	if err != nil || c.Live == nil {
		return nil, automationLiveRecord{}, api.ErrValidation("Save separate live expectations for the selected case before starting verification.")
	}
	if err := automationlive.Validate(def, *c.Live); err != nil {
		return nil, automationLiveRecord{}, api.ErrValidation(err.Error())
	}
	input := c.Simulation.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, input) != nil || int64(compact.Len()) > api.WorkflowRunInputMaxBytes {
		return nil, automationLiveRecord{}, api.ErrValidation("The saved case input exceeds the live run input limit or is invalid JSON.")
	}
	run := &WorkflowRun{ID: uuid.NewString(), AppID: appID, DeploymentID: deploymentID, WorkflowName: name,
		Input: append(json.RawMessage(nil), compact.Bytes()...), DefinitionSnapshot: cloneWorkflowJSON(record.Published)}
	if err := prepareWorkflowRun(run); err != nil {
		return nil, automationLiveRecord{}, err
	}
	hash, err := liveCaseHash(def, c)
	if err != nil {
		return nil, automationLiveRecord{}, err
	}
	test, err := json.Marshal(c.Live)
	if err != nil {
		return nil, automationLiveRecord{}, err
	}
	live := automationLiveRecord{AppID: appID, Name: name, Key: key, Fingerprint: append([]byte(nil), fingerprint...), Test: test,
		View: api.AutomationLiveVerification{RunID: run.ID, CaseName: c.Name, DeploymentID: deploymentID, InputsHash: hash,
			Version: record.Version, PublishedVersion: record.PublishedVersion, RunStatus: WorkflowRunStatusPending}}
	return run, live, nil
}

func liveRunTerminal(status string) bool {
	return status == WorkflowRunStatusSucceeded || status == WorkflowRunStatusFailed || status == WorkflowRunStatusDead
}

func liveView(live automationLiveRecord, status string, updated time.Time, resumes int, record Automation, deploymentID string) api.AutomationLiveVerification {
	// Deep-copy reports before returning caller-owned slices and pointers.
	raw, _ := json.Marshal(live.View)
	var view api.AutomationLiveVerification
	_ = json.Unmarshal(raw, &view)
	view.RunStatus = status
	def, c, err := liveCase(record, view.CaseName)
	hash, hashErr := liveCaseHash(def, c)
	view.Outdated = err != nil || hashErr != nil || view.PublishedVersion != record.PublishedVersion || !sameMemUUID(view.DeploymentID, deploymentID) || view.InputsHash != hash
	view.ReportCurrent = view.Report != nil && liveRunTerminal(status) && view.Report.RunStatus == status &&
		view.Report.ResumeCount == resumes && view.Report.RunUpdatedAt.Equal(updated)
	return view
}

func evaluateLiveReport(ctx context.Context, testJSON json.RawMessage, status string, cancelled bool, resumes int, updated time.Time, observe func(api.AutomationLiveExpectation) ([]automationlive.ObservedStep, error)) (*api.AutomationLiveReport, error) {
	if !liveRunTerminal(status) {
		return nil, nil // A parked or running workflow has no assertion verdict yet.
	}
	var test api.AutomationLiveTest
	if json.Unmarshal(testJSON, &test) != nil || len(test.Expectations) == 0 || len(test.Expectations) > api.AutomationLiveMaxExpectations {
		return nil, errors.New("invalid stored live verification")
	}
	report := &api.AutomationLiveReport{CheckedAt: time.Now().UTC(), RunStatus: status, RunUpdatedAt: updated, ResumeCount: resumes,
		Passed: !cancelled && status == automationlive.ExpectedRunStatus(test), Steps: []api.AutomationTestStepResult{}}
	if cancelled {
		report.Issue = "run_cancelled"
	} else if !report.Passed {
		report.Issue = "run_status_mismatch"
	}
	for _, e := range test.Expectations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := observe(e)
		if err != nil {
			return nil, err
		}
		result := automationlive.Compare(e, rows)
		report.Steps = append(report.Steps, result)
		report.Passed = report.Passed && result.Passed
	}
	if !report.Passed && report.Issue == "" {
		report.Issue = "assertion_failed"
	}
	raw, err := json.Marshal(report)
	if err != nil || int64(len(raw)) > api.AutomationLiveReportMaxBytes {
		return nil, ErrAutomationInvalid
	}
	return report, ctx.Err()
}
