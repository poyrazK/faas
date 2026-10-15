package main

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type testDigestComparisonSide struct {
	groups map[testFailureGroupKey]testDigestGroup
	runs   map[testFailureGroupScope][]testRunReceipt
	order  []testFailureGroupKey
}

type testDigestGroupChange struct {
	fingerprint string
	key         testFailureGroupKey
	status      string
	reason      string
	before      testDigestGroup
	after       testDigestGroup
	beforeRuns  int
	afterRuns   int
	delta       *float64
}

func buildTestDigestComparisonSide(receipts []testRunReceipt) testDigestComparisonSide {
	side := testDigestComparisonSide{groups: make(map[testFailureGroupKey]testDigestGroup), runs: make(map[testFailureGroupScope][]testRunReceipt)}
	for root, receipt := range receipts {
		var leaves []testRunReceipt
		collectTestFailureLeaves(receipt, "", &leaves)
		for _, r := range leaves {
			if r.Status == "passed" || r.Status == "failed" {
				scope := testFailureScope(r)
				side.runs[scope] = append(side.runs[scope], r)
			}
			for _, row := range collectTestDigestGroups([]testRunReceipt{r}) {
				row.root = root
				key := row.group.Key
				existing, ok := side.groups[key]
				if !ok {
					side.groups[key] = row
					side.order = append(side.order, key)
					continue
				}
				existing.group.Failures += row.group.Failures
				existing.group.Occurrences = append(existing.group.Occurrences, row.group.Occurrences...)
				if row.group.Operator == "mixed" {
					existing.group.Operator, existing.group.WorstObserved, existing.group.WorstLimit = "mixed", nil, nil
				} else {
					updateTestFailureWorst(&existing.group, &testFailureAssertionSummary{Operator: row.group.Operator, Observed: row.group.WorstObserved, Expected: row.group.WorstLimit})
				}
				side.groups[key] = existing
			}
		}
	}
	return side
}

// Compare each exact configuration independently: a changed seed distribution must
// never change a failure rate merely by changing the campaign's mixture.
func compareTestDigestGroups(before, after []testRunReceipt) []testDigestGroupChange {
	old, current := partitionTestDigestConfigurations(before), partitionTestDigestConfigurations(after)
	keys := make(map[string]bool)
	for hash := range old {
		keys[hash] = true
	}
	for hash := range current {
		keys[hash] = true
	}
	ordered := make([]string, 0, len(keys))
	for hash := range keys {
		ordered = append(ordered, hash)
	}
	sort.Strings(ordered)
	var changes []testDigestGroupChange
	for _, hash := range ordered {
		rows := compareTestDigestConfiguration(old[hash], current[hash])
		for i := range rows {
			rows[i].fingerprint = hash
		}
		changes = append(changes, rows...)
	}
	return changes
}

// Preserve top-level receipt indexes for links into the original HTML report.
func partitionTestDigestConfigurations(receipts []testRunReceipt) map[string][]testRunReceipt {
	partitions := make(map[string][]testRunReceipt)
	for _, receipt := range receipts {
		var leaves []testRunReceipt
		collectTestFailureLeaves(receipt, "", &leaves)
		for _, r := range leaves {
			hash := testDigestScopeFingerprint([]testRunReceipt{r})
			partitions[hash] = nil
		}
	}
	for hash := range partitions {
		rows := make([]testRunReceipt, len(receipts))
		for root, receipt := range receipts {
			rows[root] = filterTestDigestConfiguration(receipt, hash)
		}
		partitions[hash] = rows
	}
	return partitions
}

func filterTestDigestConfiguration(r testRunReceipt, hash string) testRunReceipt {
	if r.FaultMatrix != nil {
		matrix := *r.FaultMatrix
		matrix.Cases = append(matrix.Cases[:0:0], matrix.Cases...)
		for i := range matrix.Cases {
			matrix.Cases[i] = filterTestDigestConfiguration(matrix.Cases[i], hash)
		}
		r.FaultMatrix = &matrix
		return r
	}
	if r.Resilience != nil {
		campaign := *r.Resilience
		campaign.History = append(campaign.History[:0:0], campaign.History...)
		for i := range campaign.History {
			campaign.History[i] = filterTestDigestConfiguration(campaign.History[i], hash)
		}
		r.Resilience = &campaign
		return r
	}
	if testDigestScopeFingerprint([]testRunReceipt{r}) != hash {
		r.Status = "skipped"
	}
	return r
}

func compareTestDigestConfiguration(before, after []testRunReceipt) []testDigestGroupChange {
	old, current := buildTestDigestComparisonSide(before), buildTestDigestComparisonSide(after)
	keys := append([]testFailureGroupKey{}, old.order...)
	seen := make(map[testFailureGroupKey]bool)
	for _, key := range keys {
		seen[key] = true
	}
	for _, key := range current.order {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	var changes []testDigestGroupChange
	for _, key := range keys {
		a, b := old.groups[key], current.groups[key]
		scope := testFailureGroupScope{scenario: key.Scenario, profile: key.Profile, engine: key.Engine, caseName: key.Case}
		oldRuns, newRuns := old.runs[scope], current.runs[scope]
		change := testDigestGroupChange{key: key, before: a, after: b, beforeRuns: len(oldRuns), afterRuns: len(newRuns), status: "not_comparable"}
		change.reason = testDigestComparisonCoverage(oldRuns, newRuns, key, a.group.Failures == 0, b.group.Failures == 0)
		if change.reason == "" {
			change.status = "persisting"
			if a.group.Failures == 0 {
				change.status = "newly_observed"
			} else if b.group.Failures == 0 {
				change.status = "no_longer_observed"
			}
			change.delta = testPhaseComparisonNumber(100 * (float64(b.group.Failures)/float64(len(newRuns)) - float64(a.group.Failures)/float64(len(oldRuns))))
		}
		changes = append(changes, change)
	}
	priority := map[string]int{"newly_observed": 0, "persisting": 1, "no_longer_observed": 2, "not_comparable": 3}
	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if priority[a.status] != priority[b.status] {
			return priority[a.status] < priority[b.status]
		}
		return a.delta != nil && b.delta != nil && *a.delta > *b.delta
	})
	return changes
}

func testDigestComparisonCoverage(before, after []testRunReceipt, key testFailureGroupKey, oldAbsent, newAbsent bool) string {
	if len(before) == 0 || len(after) == 0 {
		return "matching executed scenario/profile/engine/case is absent"
	}
	oldHash, newHash := testDigestScopeFingerprint(before), testDigestScopeFingerprint(after)
	if oldHash == "" || newHash == "" || oldHash != newHash {
		return "configuration fingerprint is missing, mixed or changed"
	}
	for side, runs := range [][]testRunReceipt{before, after} {
		requirePass := (side == 0 && oldAbsent) || (side == 1 && newAbsent)
		for _, r := range runs {
			if !testDigestRunCovers(r, key, requirePass) {
				return "phase/assertion/fault coverage is incomplete, or a missing group hides an unpassed assertion"
			}
		}
	}
	return ""
}

func testDigestScopeFingerprint(runs []testRunReceipt) string {
	fingerprint := ""
	for _, r := range runs {
		hash := r.ConfigurationSHA256
		if hash == "" && r.Replay != nil {
			hash = r.Replay.ConfigurationSHA256
			if r.Replay.ReducedConfigurationSHA256 != "" {
				hash = r.Replay.ReducedConfigurationSHA256
			}
		}
		if len(hash) != 64 || (fingerprint != "" && fingerprint != hash) {
			return ""
		}
		fingerprint = hash
	}
	return fingerprint
}

// Passing runs retain a digest without becoming replayable failure receipts.
func recordTestDigestConfiguration(receipt *testRunReceipt, plan testRunPlan) {
	if plan.Prepared == nil {
		return
	}
	manifest := plan.ReplayManifest
	if manifest == nil {
		manifest = newTestReplayManifest(plan.Prepared.Name, plan.Profile, plan.ReplayCase, plan.Prepared.Scenario)
	}
	if manifest == nil {
		return
	}
	receipt.ConfigurationSHA256 = manifest.ConfigurationSHA256
	if manifest.ReducedConfigurationSHA256 != "" {
		receipt.ConfigurationSHA256 = manifest.ReducedConfigurationSHA256
	}
}

func testDigestRunCovers(r testRunReceipt, key testFailureGroupKey, requirePass bool) bool {
	load := r.Load
	if key.Step != "" {
		load = nil
		found := 0
		for _, step := range r.Steps {
			if step.Name == key.Step {
				found++
				load = step.Load
			}
		}
		if found != 1 {
			return false
		}
	}
	if load == nil {
		return false
	}
	index, count := -1, 0
	for i, phase := range load.ChaosPhases {
		if phase.Name == key.Phase {
			index, count = i, count+1
		}
	}
	if count != 1 || load.ChaosPhases[index].ActualDurationMS <= 0 {
		return false
	}
	observed, passed := testDigestAssertionCoverage(load.ChaosPhases[index], key)
	if !observed || (requirePass && !passed) {
		return false
	}
	return testDigestFaultCoverage(load.ChaosPhases, index, key.FaultKinds)
}

func testDigestFaultCoverage(phases []testLoadChaosPhaseEvidence, index int, signature string) bool {
	role := phases[index].Role
	if role == "" {
		role = testRecordedPhaseRole(index, phases)
	}
	if signature == "unavailable" {
		return (role == "baseline" || role == "control") && (phases[index].Chaos == nil || (len(phases[index].Chaos.Rules) == 0 && len(phases[index].Chaos.Matches) == 0))
	}
	if role == "recovery" {
		if index == 0 {
			return false
		}
		index--
	}
	e := phases[index].Chaos
	if e == nil || len(e.Rules) == 0 || len(e.Matches) != len(e.Rules) {
		return false
	}
	for _, match := range e.Matches {
		if match.Matches <= 0 || match.Matches < match.MinMatches {
			return false
		}
	}
	// Reuse the grouping model's canonical fault-kind signature.
	recorded := testFailureGroupingKey(testRunReceipt{}, &testFailureSummary{FaultEvidence: testFailureFaultRecords(e)})
	return recorded.FaultKinds == signature
}

func testDigestAssertionCoverage(p testLoadChaosPhaseEvidence, key testFailureGroupKey) (bool, bool) {
	switch key.Kind {
	case "threshold":
		checks := p.Thresholds
		if key.Request != "" {
			checks = nil
			for _, step := range p.Steps {
				if step.Name == key.Request {
					checks = append(checks, step.Thresholds...)
				}
			}
		}
		found, passed := 0, true
		for _, check := range checks {
			if check.Metric == key.Assertion {
				found++
				passed = passed && check.Passed
			}
		}
		return found == 1, passed
	case "http_attempts", "tcp_attempts", "per_request_attempts":
		e := p.RetryAmplification
		if key.Kind == "tcp_attempts" {
			e = p.ConnectionAttempts
		}
		if e == nil || e.Request != key.Request {
			return false, false
		}
		if key.Kind == "per_request_attempts" {
			return e.PerRequest != nil, e.PerRequest != nil && e.PerRequest.Passed
		}
		return true, e.Passed
	case "recovery":
		e := p.Recovery
		if key.Assertion == "step_recovery" {
			e = p.StepRecovery[key.Request]
		}
		return e != nil, e != nil && e.Passed
	}
	return testDigestResourceCoverage(p, key)
}

func testDigestResourceCoverage(p testLoadChaosPhaseEvidence, key testFailureGroupKey) (bool, bool) {
	switch key.Kind {
	case "request_deadline":
		e := p.RequestDeadlines[key.Request]
		return e != nil, e != nil && e.Passed
	case "circuit_breaker":
		return p.CircuitBreaker != nil, p.CircuitBreaker != nil && p.CircuitBreaker.Passed
	case "connection_leaks":
		return p.ConnectionLeaks != nil, p.ConnectionLeaks != nil && p.ConnectionLeaks.Passed
	case "pool_exhaustion":
		return p.PoolExhaustion != nil, p.PoolExhaustion != nil && p.PoolExhaustion.Passed
	}
	return false, false
}

func appendTestDigestBaseline(body string, current []testRunReceipt, path, evidence, output, currentEvidence string, limit int) (string, error) {
	if path == "" {
		if evidence != "" {
			return "", fmt.Errorf("--baseline-evidence-url requires --baseline")
		}
		return body, nil
	}
	url, err := validateTestDigestURL(evidence)
	if err != nil {
		return "", err
	}
	baseline, _, err := readTestBaselineReport(path, []string{output})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(body)
	_, _ = fmt.Fprintln(&out, "\n## Failure-group changes vs baseline")
	_, _ = fmt.Fprintln(&out, "\nCounts describe first recorded failures, not statistical confidence. No longer observed requires matching executed coverage and passing assertion evidence. Each exact configuration is compared separately; missing configurations are not comparable.")
	changes := compareTestDigestGroups(baseline, current)
	counts := make(map[string]int)
	for _, change := range changes {
		counts[change.status]++
	}
	_, _ = fmt.Fprintf(&out, "\n%d newly observed; %d persisting; %d no longer observed; %d not comparable.\n", counts["newly_observed"], counts["persisting"], counts["no_longer_observed"], counts["not_comparable"])
	for i, change := range changes {
		if i >= limit {
			_, _ = fmt.Fprintf(&out, "\n%d additional comparisons omitted.\n", len(changes)-i)
			break
		}
		writeTestDigestChange(&out, change, url, currentEvidence)
	}
	if len(changes) == 0 {
		_, _ = fmt.Fprintln(&out, "\nNo structured failure groups in either report; this does not establish matching coverage.")
	}
	return out.String(), nil
}

func writeTestDigestChange(out *strings.Builder, c testDigestGroupChange, baselineURL, currentURL string) {
	_, _ = fmt.Fprintf(out, "\n### %s: %s / %s / %s\n", c.status, testDigestText(c.key.Scenario), testDigestText(c.key.Phase), testDigestText(c.key.Assertion))
	if c.fingerprint != "" {
		_, _ = fmt.Fprintf(out, "\n- Configuration SHA-256: %s\n", testDigestText(c.fingerprint))
	}
	_, _ = fmt.Fprintf(out, "\n- Baseline: %d / %d executed runs; current: %d / %d.\n", c.before.group.Failures, c.beforeRuns, c.after.group.Failures, c.afterRuns)
	if c.reason != "" {
		_, _ = fmt.Fprintf(out, "- Not comparable: %s.\n", c.reason)
	} else {
		_, _ = fmt.Fprintf(out, "- Frequency change: %s percentage points.\n", testPhaseComparisonValue(c.delta))
		_, _ = fmt.Fprintf(out, "- Worst observed: %s -> %s; associated limits: %s -> %s.\n", testPhaseComparisonValue(c.before.group.WorstObserved), testPhaseComparisonValue(c.after.group.WorstObserved), testPhaseComparisonValue(c.before.group.WorstLimit), testPhaseComparisonValue(c.after.group.WorstLimit))
	}
	if c.before.group.Failures > 0 {
		_, _ = fmt.Fprintf(out, "- Baseline first supporting receipt: %s\n", testDigestEvidenceLink(baselineURL, c.before.root))
	} else if baselineURL != "" {
		_, _ = fmt.Fprintf(out, "- [Baseline report](<%s>)\n", baselineURL)
	}
	if c.after.group.Failures > 0 {
		_, _ = fmt.Fprintf(out, "- Current first supporting receipt: %s\n", testDigestEvidenceLink(currentURL, c.after.root))
	} else if currentURL != "" {
		_, _ = fmt.Fprintf(out, "- [Current report](<%s>)\n", currentURL)
	}
	occurrences, label := c.after.group.Occurrences, "Current"
	if len(occurrences) == 0 {
		occurrences, label = c.before.group.Occurrences, "Baseline"
	}
	if len(occurrences) > 0 && occurrences[0].Replay != "" {
		command := occurrences[0].Replay
		if len(command) <= 4096 && strings.IndexFunc(command, unicode.IsControl) < 0 {
			_, _ = fmt.Fprintf(out, "- %s replay example (replace original path placeholders): <code>%s</code>\n", label, testDigestReplayText(command))
		}
	}
}
