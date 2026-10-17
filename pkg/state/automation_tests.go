package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationchecks"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AutomationTestStore runs explicitly saved mocks against the saved draft. It
// records observations without changing draft versions or runtime admission.
type AutomationTestStore interface {
	RunAutomationTests(context.Context, string, string, int64, api.Plan) (api.AutomationTestReport, error)
}

func validateAutomationTestSuite(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	// PostgreSQL JSONB inserts insignificant spaces when returning stored JSON.
	// Apply content limits to compact JSON, consistently before and after storage.
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || int64(compact.Len()) > api.AutomationTestSuiteMaxBytes {
		return ErrAutomationInvalid
	}
	raw = compact.Bytes()
	var suite api.AutomationTestSuite
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&suite) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrAutomationInvalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrAutomationInvalid
	}
	if len(suite.Cases) > api.AutomationPublishCheckMaxScenarios {
		return ErrAutomationInvalid
	}
	sample, err := json.Marshal(suite.Sample)
	if err != nil || int64(len(sample)) > api.AutomationSimulationRequestMaxBytes {
		return ErrAutomationInvalid
	}
	cases, err := json.Marshal(suite.Cases)
	if err != nil || int64(len(cases)) > api.AutomationPublishCheckMaxBytes {
		return ErrAutomationInvalid
	}
	seen := map[string]bool{}
	for _, c := range suite.Cases {
		if c.Name == "" || strings.TrimSpace(c.Name) != c.Name || len(c.Name) > api.AutomationTestCaseNameMaxBytes || strings.IndexFunc(c.Name, unicode.IsControl) >= 0 || seen[c.Name] || len(c.Expectations) > api.AutomationPublishCheckMaxExpectations {
			return ErrAutomationInvalid
		}
		seen[c.Name] = true
		raw, err := json.Marshal(c.Simulation)
		if err != nil || int64(len(raw)) > api.AutomationSimulationRequestMaxBytes {
			return ErrAutomationInvalid
		}
	}
	return nil
}

func evaluateAutomationTests(ctx context.Context, store AutomationStore, outbound WorkflowOutboundStore, appID, name string, version int64, plan api.Plan) (api.AutomationTestReport, error) {
	if version <= 0 {
		return api.AutomationTestReport{}, ErrAutomationVersionConflict
	}
	records, err := store.ListAutomations(ctx, appID)
	if err != nil {
		return api.AutomationTestReport{}, err
	}
	for _, record := range records {
		if record.Name != name {
			continue
		}
		if record.Version != version {
			return api.AutomationTestReport{}, ErrAutomationVersionConflict
		}
		if len(record.TestSuite) == 0 {
			return api.AutomationTestReport{}, ErrAutomationInvalid
		}
		var def api.WorkflowSpec
		var suite api.AutomationTestSuite
		if json.Unmarshal(record.Draft, &def) != nil || validateAutomationTestSuite(record.TestSuite) != nil || json.Unmarshal(record.TestSuite, &suite) != nil || len(suite.Cases) == 0 {
			return api.AutomationTestReport{}, ErrAutomationInvalid
		}
		if err := outbound.ValidateWorkflowOutboundBindings(ctx, appID, def); err != nil {
			return api.AutomationTestReport{}, err
		}
		return simulateAutomationTestSuite(ctx, def, suite, version, plan)
	}
	return api.AutomationTestReport{}, ErrNotFound
}

func simulateAutomationTestSuite(ctx context.Context, def api.WorkflowSpec, suite api.AutomationTestSuite, version int64, plan api.Plan) (api.AutomationTestReport, error) {
	hash, err := api.AutomationTestInputsHash(def, suite)
	if err != nil {
		return api.AutomationTestReport{}, ErrAutomationInvalid
	}
	report := api.AutomationTestReport{CheckedVersion: version, CheckedAt: time.Now().UTC(), InputsHash: hash, Passed: true, Cases: []api.AutomationTestCaseResult{}}
	for _, c := range suite.Cases {
		if err := ctx.Err(); err != nil {
			return api.AutomationTestReport{}, err
		}
		result := evaluateAutomationTestCase(ctx, def, c, plan)
		report.Passed = report.Passed && result.Passed
		report.Cases = append(report.Cases, result)
	}
	if err := ctx.Err(); err != nil {
		return api.AutomationTestReport{}, err
	}
	raw, err := json.Marshal(report)
	if err != nil || int64(len(raw)) > api.AutomationTestReportMaxBytes {
		return api.AutomationTestReport{}, ErrAutomationInvalid
	}
	return report, nil
}

func evaluateAutomationTestCase(ctx context.Context, def api.WorkflowSpec, c api.AutomationTestCase, plan api.Plan) api.AutomationTestCaseResult {
	result := api.AutomationTestCaseResult{Name: c.Name, Steps: []api.AutomationTestStepResult{}}
	if automationchecks.ValidateExpectations(def, c.Expectations) != nil {
		result.Issue = "invalid_expectations"
		return result
	}
	response, err := SimulateAutomation(ctx, c.Simulation.Simulation(def), plan)
	if err != nil {
		result.Issue = "simulation_rejected"
		return result
	}
	result.Passed = automationchecks.Passes(c.Expectations, response)
	switch {
	case !response.DefinitionValid:
		result.Issue = "definition_invalid"
	case !response.Complete || len(response.Issues) > 0:
		result.Issue = "simulation_incomplete"
	case !result.Passed:
		result.Issue = "assertion_failed"
	}
	// Per-step results describe the assertion independently of another blocked
	// branch. Only the overall result can certify a complete simulation.
	response.DefinitionValid, response.Complete, response.Issues = true, true, nil
	for _, e := range c.Expectations {
		if len(result.Steps) >= api.AutomationTestStepResultsMax {
			result.StepsTruncated = true
			break
		}
		mismatches := automationchecks.MismatchFields(e, response.Trace)
		step := api.AutomationTestStepResult{Step: e.Step, Loop: e.Loop, ItemIndex: e.ItemIndex, Passed: len(mismatches) == 0, Mismatches: mismatches}
		for _, row := range response.Trace {
			if (e.Loop == "" && row.StepName == e.Step && row.ParentStep == "" && row.ItemIndex == nil) || (e.Loop != "" && row.ParentStep == e.Loop && e.ItemIndex != nil && row.ItemIndex != nil && *e.ItemIndex == *row.ItemIndex) {
				step.State = row.State
				break
			}
		}
		result.Steps = append(result.Steps, step)
	}
	return result
}

func (m *MemStore) RunAutomationTests(ctx context.Context, appID, name string, version int64, plan api.Plan) (api.AutomationTestReport, error) {
	report, err := evaluateAutomationTests(ctx, m, m, appID, name, version, plan)
	if err != nil {
		return report, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return api.AutomationTestReport{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationTestReport{}, err
	}
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return api.AutomationTestReport{}, ErrNotFound
	}
	account, ok := m.accounts[app.AccountID]
	if !ok {
		return api.AutomationTestReport{}, ErrNotFound
	}
	if err := automationAccountGate(account.Plan, account.Active(), "tests"); err != nil {
		return api.AutomationTestReport{}, err
	}
	current, ok := m.automations[appID+"/"+name]
	if !ok || current.Version != version {
		return api.AutomationTestReport{}, ErrAutomationVersionConflict
	}
	current.TestReport = raw
	m.automations[appID+"/"+name] = current
	return report, nil
}

func (s *PgStore) RunAutomationTests(ctx context.Context, appID, name string, version int64, plan api.Plan) (api.AutomationTestReport, error) {
	report, err := evaluateAutomationTests(ctx, s, s, appID, name, version, plan)
	if err != nil {
		return report, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return api.AutomationTestReport{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.AutomationTestReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.AutomationTestReport{}, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return api.AutomationTestReport{}, err
	}
	if target.AppStatus == string(AppDeleted) {
		return api.AutomationTestReport{}, ErrNotFound
	}
	if err := automationAccountGate(api.Plan(target.Plan), (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid, "tests"); err != nil {
		return api.AutomationTestReport{}, err
	}
	count, err := q.SaveAutomationTestReport(ctx, tx, sqlc.SaveAutomationTestReportParams{AppID: mustPgUUID(appID), Name: name, Version: version, TestReport: raw})
	if err != nil {
		return api.AutomationTestReport{}, err
	}
	if count != 1 {
		return api.AutomationTestReport{}, ErrAutomationVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return api.AutomationTestReport{}, err
	}
	return report, nil
}
