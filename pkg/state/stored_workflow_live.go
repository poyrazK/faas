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
	"github.com/onebox-faas/faas/pkg/storedworkflowlive"
)

type StoredWorkflowLiveStore interface {
	StartStoredWorkflowLiveVerification(context.Context, string, string, string, api.StartStoredWorkflowLiveVerificationRequest, bool) (api.StoredWorkflowLiveVerification, bool, error)
	ListStoredWorkflowLiveVerifications(context.Context, string, string) (api.ListStoredWorkflowLiveVerificationsResponse, error)
	GetStoredWorkflowLiveVerification(context.Context, string, string, string) (api.StoredWorkflowLiveVerification, error)
	CheckStoredWorkflowLiveVerification(context.Context, string, string, string) (api.StoredWorkflowLiveVerification, error)
}

type storedWorkflowLiveRecord struct {
	AppID, Name, Key string
	Fingerprint      []byte
	Test             json.RawMessage
	View             api.StoredWorkflowLiveVerification
}

func validateLiveStart(req *api.StartStoredWorkflowLiveVerificationRequest, key string) ([]byte, error) {
	if !req.ConfirmRealActions {
		return nil, api.ErrValidation("Confirm that live verification may perform real actions in your app and connected services.")
	}
	id, err := uuid.Parse(req.ExpectedDeploymentID)
	if err != nil || id == uuid.Nil || req.ExpectedVersion <= 0 || req.ExpectedPublishedVersion <= 0 || req.ExpectedPublishedVersion > req.ExpectedVersion {
		return nil, ErrStoredWorkflowVersionConflict
	}
	if key == "" || strings.TrimSpace(key) != key || len(key) > WorkflowRunIdempotencyKeyMaxBytes || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return nil, api.ErrValidation("A valid Idempotency-Key is required for live verification.")
	}
	if req.CaseName == "" || strings.TrimSpace(req.CaseName) != req.CaseName || len(req.CaseName) > api.StoredWorkflowTestCaseNameMaxBytes || strings.IndexFunc(req.CaseName, unicode.IsControl) >= 0 {
		return nil, ErrStoredWorkflowInvalid
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	return hash[:], nil
}

func liveCase(record StoredWorkflow, name string) (api.WorkflowSpec, api.StoredWorkflowTestCase, error) {
	var def api.WorkflowSpec
	var suite api.StoredWorkflowTestSuite
	if len(record.Published) == 0 || json.Unmarshal(record.Published, &def) != nil || json.Unmarshal(record.TestSuite, &suite) != nil {
		return def, api.StoredWorkflowTestCase{}, ErrStoredWorkflowInvalid
	}
	for _, c := range suite.Cases {
		if c.Name == name {
			return def, c, nil
		}
	}
	return def, api.StoredWorkflowTestCase{}, ErrStoredWorkflowInvalid
}

func liveCaseHash(def api.WorkflowSpec, c api.StoredWorkflowTestCase) (string, error) {
	return api.StoredWorkflowTestInputsHash(def, api.StoredWorkflowTestSuite{Cases: []api.StoredWorkflowTestCase{c}})
}

func prepareLiveVerification(appID, name, key, deploymentID string, record StoredWorkflow, req api.StartStoredWorkflowLiveVerificationRequest, fingerprint []byte) (*WorkflowRun, storedWorkflowLiveRecord, error) {
	if record.Version != req.ExpectedVersion || record.PublishedVersion != req.ExpectedPublishedVersion || !sameMemUUID(deploymentID, req.ExpectedDeploymentID) {
		return nil, storedWorkflowLiveRecord{}, ErrStoredWorkflowVersionConflict
	}
	def, c, err := liveCase(record, req.CaseName)
	if err != nil || c.Live == nil {
		return nil, storedWorkflowLiveRecord{}, api.ErrValidation("Save separate live expectations for the selected case before starting verification.")
	}
	if err := storedworkflowlive.Validate(def, *c.Live); err != nil {
		return nil, storedWorkflowLiveRecord{}, api.ErrValidation(err.Error())
	}
	input := c.Simulation.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, input) != nil || int64(compact.Len()) > api.WorkflowRunInputMaxBytes {
		return nil, storedWorkflowLiveRecord{}, api.ErrValidation("The saved case input exceeds the live run input limit or is invalid JSON.")
	}
	run := &WorkflowRun{ID: uuid.NewString(), AppID: appID, DeploymentID: deploymentID, WorkflowName: name,
		Input: append(json.RawMessage(nil), compact.Bytes()...), DefinitionSnapshot: cloneWorkflowJSON(record.Published)}
	if err := prepareWorkflowRun(run); err != nil {
		return nil, storedWorkflowLiveRecord{}, err
	}
	hash, err := liveCaseHash(def, c)
	if err != nil {
		return nil, storedWorkflowLiveRecord{}, err
	}
	test, err := json.Marshal(c.Live)
	if err != nil {
		return nil, storedWorkflowLiveRecord{}, err
	}
	live := storedWorkflowLiveRecord{AppID: appID, Name: name, Key: key, Fingerprint: append([]byte(nil), fingerprint...), Test: test,
		View: api.StoredWorkflowLiveVerification{RunID: run.ID, CaseName: c.Name, DeploymentID: deploymentID, InputsHash: hash,
			Version: record.Version, PublishedVersion: record.PublishedVersion, RunStatus: WorkflowRunStatusPending}}
	return run, live, nil
}

func liveRunTerminal(status string) bool {
	return status == WorkflowRunStatusSucceeded || status == WorkflowRunStatusFailed || status == WorkflowRunStatusDead
}

func liveView(live storedWorkflowLiveRecord, status string, updated time.Time, resumes int, record StoredWorkflow, deploymentID string) api.StoredWorkflowLiveVerification {
	// Deep-copy reports before returning caller-owned slices and pointers.
	raw, _ := json.Marshal(live.View)
	var view api.StoredWorkflowLiveVerification
	_ = json.Unmarshal(raw, &view)
	view.RunStatus = status
	def, c, err := liveCase(record, view.CaseName)
	hash, hashErr := liveCaseHash(def, c)
	view.Outdated = err != nil || hashErr != nil || view.PublishedVersion != record.PublishedVersion || !sameMemUUID(view.DeploymentID, deploymentID) || view.InputsHash != hash
	view.ReportCurrent = view.Report != nil && liveRunTerminal(status) && view.Report.RunStatus == status &&
		view.Report.ResumeCount == resumes && view.Report.RunUpdatedAt.Equal(updated)
	return view
}

func evaluateLiveReport(ctx context.Context, testJSON json.RawMessage, status string, cancelled bool, resumes int, updated time.Time, observe func(api.StoredWorkflowLiveExpectation) ([]storedworkflowlive.ObservedStep, error)) (*api.StoredWorkflowLiveReport, error) {
	if !liveRunTerminal(status) {
		return nil, nil // A parked or running workflow has no assertion verdict yet.
	}
	var test api.StoredWorkflowLiveTest
	if json.Unmarshal(testJSON, &test) != nil || len(test.Expectations) == 0 || len(test.Expectations) > api.StoredWorkflowLiveMaxExpectations {
		return nil, errors.New("invalid stored live verification")
	}
	report := &api.StoredWorkflowLiveReport{CheckedAt: time.Now().UTC(), RunStatus: status, RunUpdatedAt: updated, ResumeCount: resumes,
		Passed: !cancelled && status == storedworkflowlive.ExpectedRunStatus(test), Steps: []api.StoredWorkflowTestStepResult{}}
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
		result := storedworkflowlive.Compare(e, rows)
		report.Steps = append(report.Steps, result)
		report.Passed = report.Passed && result.Passed
	}
	if !report.Passed && report.Issue == "" {
		report.Issue = "assertion_failed"
	}
	raw, err := json.Marshal(report)
	if err != nil || int64(len(raw)) > api.StoredWorkflowLiveReportMaxBytes {
		return nil, ErrStoredWorkflowInvalid
	}
	return report, ctx.Err()
}
