package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const testBaselineReportLimit = 64 << 20

type testBaselineKey struct {
	Scenario, Engine, Profile, Case string
	Attempt                         int
}

func testReceiptBaselineKey(receipt testRunReceipt) testBaselineKey {
	return testBaselineKey{receipt.Scenario, receipt.Engine, receipt.Profile, receipt.Case, receipt.Attempt}
}

func prepareTestBaselines(path string, plans []testRunPlan, outputs ...string) (string, error) {
	for i := range plans {
		if plans[i].Prepared.Load == nil {
			continue
		}
		workload, err := testPlanWorkload(plans[i])
		if err != nil {
			return "", err
		}
		plans[i].Workload = workload
	}
	if path == "" {
		return "", nil
	}
	receipts, digest, err := readTestBaselineReport(path, outputs)
	if err != nil {
		return "", err
	}
	index := make(map[testBaselineKey]*testRunReceipt, len(receipts))
	for i := range receipts {
		receipt := &receipts[i]
		key := testReceiptBaselineKey(*receipt)
		if _, exists := index[key]; exists {
			return "", fmt.Errorf("baseline contains duplicate run %s/%s/%s#%d", key.Scenario, key.Profile, key.Case, key.Attempt)
		}
		index[key] = receipt
	}
	for i := range plans {
		plan := &plans[i]
		p := plan.Prepared
		key := testBaselineKey{p.Name, p.Engine, plan.Profile, plan.Case.Name, plan.Attempt}
		baseline, ok := index[key]
		if !ok {
			return "", fmt.Errorf("baseline is missing %s/%s/%s#%d; record the same cases and repeats", key.Scenario, key.Profile, key.Case, key.Attempt)
		}
		if err := validateTestBaselineRun(*plan, baseline); err != nil {
			return "", fmt.Errorf("baseline %s/%s/%s#%d: %w", key.Scenario, key.Profile, key.Case, key.Attempt, err)
		}
		plan.Baseline = baseline
	}
	return digest, nil
}

func readTestBaselineReport(path string, outputs []string) ([]testRunReceipt, string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("open baseline report: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, "", fmt.Errorf("inspect baseline report: %w", err)
	}
	for _, output := range outputs {
		if output == "" {
			continue
		}
		outputInfo, err := os.Stat(output)
		if err != nil && !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("inspect report destination: %w", err)
		}
		if err == nil && os.SameFile(info, outputInfo) {
			return nil, "", errors.New("report outputs must not overwrite the baseline report")
		}
	}
	body, err := io.ReadAll(io.LimitReader(file, testBaselineReportLimit+1))
	if err != nil {
		return nil, "", fmt.Errorf("read baseline report: %w", err)
	}
	if len(body) > testBaselineReportLimit {
		return nil, "", errors.New("baseline report exceeds 64 MiB")
	}
	var receipts []testRunReceipt
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&receipts); err != nil {
		return nil, "", fmt.Errorf("decode baseline JSON report: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, "", errors.New("baseline report must contain one JSON array")
	}
	if len(receipts) == 0 {
		return nil, "", errors.New("baseline report contains no runs")
	}
	digest := sha256.Sum256(body)
	return receipts, hex.EncodeToString(digest[:]), nil
}

func validateTestBaselineRun(plan testRunPlan, receipt *testRunReceipt) error {
	if plan.Prepared.Load == nil || plan.Prepared.Engine != "local" {
		return errors.New("baseline comparison requires native local load runs")
	}
	if receipt.Status != "passed" || receipt.Error != "" || receipt.CleanupError != "" || receipt.Load == nil || receipt.Load.Status != "passed" {
		return errors.New("only successful load runs with successful cleanup can be baselines")
	}
	workload := receipt.Load.Workload
	if workload == nil || workload.Version != 1 || workload.SHA256 == "" {
		return errors.New("report lacks supported workload metadata; record a new baseline with this CLI")
	}
	if workload.SHA256 != plan.Workload.SHA256 || workload.Label != plan.Workload.Label || !testLoadScheduleMatches(receipt.Load, plan.Prepared.Load) {
		return errors.New("workload mismatch; use matching request templates, data field types, app mode, workload label, timeout and load schedule")
	}
	if err := validateTestBenchmarkLoad(receipt.Load, plan.Prepared.Scenario); err != nil {
		return err
	}
	cfg := plan.Prepared.Load.Regression
	if receipt.Load.Latency.Samples < cfg.MinSamples {
		return fmt.Errorf("aggregate has %d samples; needs at least %d", receipt.Load.Latency.Samples, cfg.MinSamples)
	}
	for _, step := range receipt.Load.Steps {
		if min := cfg.stepLimits(step.Name).MinSamples; step.Latency.Samples < min {
			return fmt.Errorf("step %s has %d samples; needs at least %d", step.Name, step.Latency.Samples, min)
		}
	}
	return nil
}

func testLoadScheduleMatches(actual *testLoadEvidence, cfg *testLoadConfig) bool {
	want := newTestLoadEvidence(cfg)
	if actual.Mode != want.Mode || actual.VUs != want.VUs || actual.MaxVUs != want.MaxVUs || actual.IterationLimit != want.IterationLimit || actual.RequestedDurationMS != want.RequestedDurationMS || actual.RequestLimit != want.RequestLimit || actual.PacingMS != want.PacingMS || len(actual.Stages) != len(want.Stages) {
		return false
	}
	if (actual.Arrival == nil) != (want.Arrival == nil) {
		return false
	}
	if actual.Arrival != nil && (actual.Arrival.TargetRate != want.Arrival.TargetRate || actual.Arrival.Planned != want.Arrival.Planned) {
		return false
	}
	for i, stage := range actual.Stages {
		w := want.Stages[i]
		if stage.Index != w.Index || stage.StartVUs != w.StartVUs || stage.TargetVUs != w.TargetVUs || stage.DurationMS != w.DurationMS {
			return false
		}
	}
	return true
}

func validateTestBenchmarkLoad(load *testLoadEvidence, scenario testScenario) error {
	if load.IterationsInterrupted != 0 || load.IterationsCompleted != load.IterationsStarted || load.IterationsStarted <= 0 || (load.Arrival != nil && (load.Arrival.Dropped != 0 || load.Arrival.Scheduled != load.Arrival.Planned || load.IterationsStarted != load.Arrival.Planned)) {
		return errors.New("load run was incomplete or dropped arrivals")
	}
	if load.IterationLimit > 0 && load.IterationsCompleted != load.IterationLimit {
		return errors.New("load run did not complete its iteration budget")
	}
	if err := testBenchmarkMetricsValid(load.testLoadMetrics); err != nil {
		return fmt.Errorf("aggregate: %w", err)
	}
	steps := append(append([]testHTTPRequest{}, scenario.Requests...), scenario.Checks...)
	if len(load.Steps) != len(steps) {
		return errors.New("report HTTP steps do not match the declared journey")
	}
	requests, failures := 0, 0
	for i, step := range load.Steps {
		if step.Name != steps[i].Name || step.Method != steps[i].Method || step.Path != strings.SplitN(steps[i].Path, "?", 2)[0] {
			return errors.New("report HTTP steps do not match the declared journey")
		}
		if err := testBenchmarkMetricsValid(step.testLoadMetrics); err != nil {
			return fmt.Errorf("step %s: %w", step.Name, err)
		}
		requests += step.Requests
		failures += step.Failures
	}
	if requests != load.Requests || failures != load.Failures {
		return errors.New("aggregate request/failure counts do not match the steps")
	}
	return nil
}

func applyTestBaseline(plan testRunPlan, receipt *testRunReceipt, digest string) {
	if receipt.Load != nil {
		receipt.Load.Workload = plan.Workload
	}
	if plan.Baseline == nil {
		return
	}
	if receipt.Status != "passed" || receipt.Load == nil || receipt.Load.Status != "passed" {
		receipt.Baseline = &testBaselineEvidence{Status: "not_compared", ReportSHA256: digest, Error: "current run did not pass execution and cleanup"}
		return
	}
	if err := validateTestBenchmarkLoad(receipt.Load, plan.Prepared.Scenario); err != nil {
		receipt.Baseline = &testBaselineEvidence{Status: "failed", ReportSHA256: digest, Error: "invalid current benchmark: " + err.Error()}
	} else {
		receipt.Baseline = compareTestBaseline(plan.Baseline.Load, receipt.Load, plan.Prepared.Load.Regression, digest)
		if receipt.Baseline.Status == "failed" {
			receipt.Baseline.Error = testBaselineFailure(receipt.Baseline)
		}
	}
	if receipt.Baseline.Status == "failed" {
		receipt.Status, receipt.Error = "failed", receipt.Baseline.Error
	}
}
