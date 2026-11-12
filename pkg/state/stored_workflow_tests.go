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
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/storedworkflowchecks"
)

// StoredWorkflowTestStore runs explicitly saved mocks against the saved draft. It
// records observations without changing draft versions or runtime admission.
type StoredWorkflowTestStore interface {
	RunStoredWorkflowTests(context.Context, string, string, int64, api.Plan) (api.StoredWorkflowTestReport, error)
}

func validateStoredWorkflowTestSuite(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	// PostgreSQL JSONB inserts insignificant spaces when returning stored JSON.
	// Apply content limits to compact JSON, consistently before and after storage.
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil || int64(compact.Len()) > api.StoredWorkflowTestSuiteMaxBytes {
		return ErrStoredWorkflowInvalid
	}
	raw = compact.Bytes()
	var suite api.StoredWorkflowTestSuite
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&suite) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ErrStoredWorkflowInvalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrStoredWorkflowInvalid
	}
	if len(suite.Cases) > api.StoredWorkflowPublishCheckMaxScenarios {
		return ErrStoredWorkflowInvalid
	}
	sample, err := json.Marshal(suite.Sample)
	if err != nil || int64(len(sample)) > api.StoredWorkflowSimulationRequestMaxBytes {
		return ErrStoredWorkflowInvalid
	}
	cases, err := json.Marshal(suite.Cases)
	if err != nil || int64(len(cases)) > api.StoredWorkflowPublishCheckMaxBytes {
		return ErrStoredWorkflowInvalid
	}
	seen := map[string]bool{}
	for _, c := range suite.Cases {
		if c.Name == "" || strings.TrimSpace(c.Name) != c.Name || len(c.Name) > api.StoredWorkflowTestCaseNameMaxBytes || strings.IndexFunc(c.Name, unicode.IsControl) >= 0 || seen[c.Name] || len(c.Expectations) > api.StoredWorkflowPublishCheckMaxExpectations {
			return ErrStoredWorkflowInvalid
		}
		seen[c.Name] = true
		raw, err := json.Marshal(c.Simulation)
		if err != nil || int64(len(raw)) > api.StoredWorkflowSimulationRequestMaxBytes {
			return ErrStoredWorkflowInvalid
		}
	}
	return nil
}

func evaluateStoredWorkflowTests(ctx context.Context, store StoredWorkflowStore, outbound WorkflowOutboundStore, appID, name string, version int64, plan api.Plan) (api.StoredWorkflowTestReport, error) {
	if version <= 0 {
		return api.StoredWorkflowTestReport{}, ErrStoredWorkflowVersionConflict
	}
	records, err := store.ListStoredWorkflows(ctx, appID)
	if err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	for _, record := range records {
		if record.Name != name {
			continue
		}
		if record.Version != version {
			return api.StoredWorkflowTestReport{}, ErrStoredWorkflowVersionConflict
		}
		if len(record.TestSuite) == 0 {
			return api.StoredWorkflowTestReport{}, ErrStoredWorkflowInvalid
		}
		var def api.WorkflowSpec
		var suite api.StoredWorkflowTestSuite
		if json.Unmarshal(record.Draft, &def) != nil || validateStoredWorkflowTestSuite(record.TestSuite) != nil || json.Unmarshal(record.TestSuite, &suite) != nil || len(suite.Cases) == 0 {
			return api.StoredWorkflowTestReport{}, ErrStoredWorkflowInvalid
		}
		if err := outbound.ValidateWorkflowOutboundBindings(ctx, appID, def); err != nil {
			return api.StoredWorkflowTestReport{}, err
		}
		return simulateStoredWorkflowTestSuite(ctx, def, suite, version, plan)
	}
	return api.StoredWorkflowTestReport{}, ErrNotFound
}

func simulateStoredWorkflowTestSuite(ctx context.Context, def api.WorkflowSpec, suite api.StoredWorkflowTestSuite, version int64, plan api.Plan) (api.StoredWorkflowTestReport, error) {
	hash, err := api.StoredWorkflowTestInputsHash(def, suite)
	if err != nil {
		return api.StoredWorkflowTestReport{}, ErrStoredWorkflowInvalid
	}
	report := api.StoredWorkflowTestReport{CheckedVersion: version, CheckedAt: time.Now().UTC(), InputsHash: hash, Passed: true, Cases: []api.StoredWorkflowTestCaseResult{}}
	for _, c := range suite.Cases {
		if err := ctx.Err(); err != nil {
			return api.StoredWorkflowTestReport{}, err
		}
		result := evaluateStoredWorkflowTestCase(ctx, def, c, plan)
		report.Passed = report.Passed && result.Passed
		report.Cases = append(report.Cases, result)
	}
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	raw, err := json.Marshal(report)
	if err != nil || int64(len(raw)) > api.StoredWorkflowTestReportMaxBytes {
		return api.StoredWorkflowTestReport{}, ErrStoredWorkflowInvalid
	}
	return report, nil
}

func evaluateStoredWorkflowTestCase(ctx context.Context, def api.WorkflowSpec, c api.StoredWorkflowTestCase, plan api.Plan) api.StoredWorkflowTestCaseResult {
	result := api.StoredWorkflowTestCaseResult{Name: c.Name, Steps: []api.StoredWorkflowTestStepResult{}}
	if storedworkflowchecks.ValidateExpectations(def, c.Expectations) != nil {
		result.Issue = "invalid_expectations"
		return result
	}
	response, err := SimulateStoredWorkflow(ctx, c.Simulation.Simulation(def), plan)
	if err != nil {
		result.Issue = "simulation_rejected"
		return result
	}
	result.Passed = storedworkflowchecks.Passes(c.Expectations, response)
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
		if len(result.Steps) >= api.StoredWorkflowTestStepResultsMax {
			result.StepsTruncated = true
			break
		}
		mismatches := storedworkflowchecks.MismatchFields(e, response.Trace)
		step := api.StoredWorkflowTestStepResult{Step: e.Step, Loop: e.Loop, ItemIndex: e.ItemIndex, Passed: len(mismatches) == 0, Mismatches: mismatches}
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

func (m *MemStore) RunStoredWorkflowTests(ctx context.Context, appID, name string, version int64, plan api.Plan) (api.StoredWorkflowTestReport, error) {
	report, err := evaluateStoredWorkflowTests(ctx, m, m, appID, name, version, plan)
	if err != nil {
		return report, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return api.StoredWorkflowTestReport{}, ErrNotFound
	}
	account, ok := m.accounts[app.AccountID]
	if !ok {
		return api.StoredWorkflowTestReport{}, ErrNotFound
	}
	if err := storedWorkflowAccountGate(account.Plan, account.Active(), "tests"); err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	current, ok := m.storedWorkflows[appID+"/"+name]
	if !ok || current.Version != version {
		return api.StoredWorkflowTestReport{}, ErrStoredWorkflowVersionConflict
	}
	current.TestReport = raw
	m.storedWorkflows[appID+"/"+name] = current
	return report, nil
}

func (s *PgStore) RunStoredWorkflowTests(ctx context.Context, appID, name string, version int64, plan api.Plan) (api.StoredWorkflowTestReport, error) {
	report, err := evaluateStoredWorkflowTests(ctx, s, s, appID, name, version, plan)
	if err != nil {
		return report, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID))
	if err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	if target.AppStatus == string(AppDeleted) {
		return api.StoredWorkflowTestReport{}, ErrNotFound
	}
	if err := storedWorkflowAccountGate(api.Plan(target.Plan), (target.AccountStatus == "active" || target.AccountStatus == "past_due") && !target.AbuseHoldAt.Valid, "tests"); err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	count, err := q.SaveStoredWorkflowTestReport(ctx, tx, sqlc.SaveStoredWorkflowTestReportParams{AppID: mustPgUUID(appID), Name: name, Version: version, TestReport: raw})
	if err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	if count != 1 {
		return api.StoredWorkflowTestReport{}, ErrStoredWorkflowVersionConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return api.StoredWorkflowTestReport{}, err
	}
	return report, nil
}
