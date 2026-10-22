package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"

	"github.com/onebox-faas/faas/pkg/chaos"
)

var testCorrelationRunPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

const testRequestAttemptsHeader = "X-Gregale-Test-Dependency-Attempts"

type testRequestAttemptSample struct {
	ID       string
	Expected *int64
}
type testRequestAttemptViolation struct {
	RequestID string `json:"request_id"`
	Attempts  int64  `json:"attempts"`
}
type testRequestAttemptBucket struct {
	Attempts int64 `json:"attempts"`
	Requests int   `json:"requests"`
}
type testPerRequestAttemptEvidence struct {
	Passed               bool                          `json:"passed"`
	Requests             int                           `json:"requests"`
	Correlated           int                           `json:"correlated"`
	UncorrelatedAttempts int64                         `json:"uncorrelated_attempts"`
	MaxAttempts          int64                         `json:"max_attempts"`
	Limit                int64                         `json:"limit"`
	Capacity             int                           `json:"capacity"`
	Overflow             int                           `json:"overflow"`
	ViolationCount       int                           `json:"violation_count"`
	Violations           []testRequestAttemptViolation `json:"violations,omitempty"`
	Distribution         []testRequestAttemptBucket    `json:"distribution,omitempty"`
	Error                string                        `json:"error,omitempty"`
}

func withTestRequestCorrelation(load *testLoadConfig) (*testLoadConfig, error) {
	copyLoad := *load
	copyLoad.ChaosSchedule = slices.Clone(load.ChaosSchedule)
	for i := range copyLoad.ChaosSchedule {
		phase := &copyLoad.ChaosSchedule[i]
		if phase.RetryAmplification == nil || !phase.RetryAmplification.PerRequest {
			continue
		}
		if len(phase.Rules) != 1 {
			return nil, errors.New("per-request attempts require one HTTP rule")
		}
		var nonce [8]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		phase.Rules = slices.Clone(phase.Rules)
		phase.Rules[0].CorrelationScope = hex.EncodeToString(nonce[:])
	}
	return &copyLoad, nil
}

func (c *testLoadCollector) correlationID(cfg *testLoadConfig, name, runID string, atPhase int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if atPhase < 0 || atPhase >= len(cfg.ChaosSchedule) {
		return ""
	}
	phase := cfg.ChaosSchedule[atPhase]
	budget := phase.RetryAmplification
	if budget == nil || !budget.PerRequest || budget.Request != name || len(phase.Rules) != 1 {
		return ""
	}
	state := &c.chaosPhases[atPhase]
	state.correlationSlots++
	if state.correlationSlots > chaos.MaxRequestCorrelationSlots {
		state.correlationOverflow++
		return ""
	}
	return chaos.RequestCorrelationID(runID, phase.Rules[0].CorrelationScope, state.correlationSlots)
}

func readTestAttemptReceipt(header http.Header) *int64 {
	values := header.Values(testRequestAttemptsHeader)
	if len(values) != 1 {
		return nil
	}
	count, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || count < 0 || count > 1<<20 || strconv.FormatInt(count, 10) != values[0] {
		return nil
	}
	return &count
}

func evaluateTestPerRequestAttempts(phase testLoadChaosPhaseEvidence, rule chaos.Rule, limit float64, expectedRequests int) *testPerRequestAttemptEvidence {
	e := &testPerRequestAttemptEvidence{Requests: expectedRequests, Limit: int64(limit), Capacity: chaos.MaxRequestCorrelationSlots, Overflow: phase.CorrelationOverflow}
	fail := func(message string) *testPerRequestAttemptEvidence { e.Error = message; return e }
	if phase.Chaos == nil || len(phase.Chaos.Matches) != 1 || rule.CorrelationScope == "" {
		return fail("correlation evidence unavailable")
	}
	counts := phase.Chaos.CorrelationCounts
	support, ok := counts[chaos.CorrelationSupportID(rule)]
	if !ok || support < 0 || support != phase.Chaos.Matches[0].Matches {
		return fail("complete gateway correlation support evidence unavailable")
	}
	e.UncorrelatedAttempts = counts[chaos.UncorrelatedAttemptID(rule)]
	if e.UncorrelatedAttempts != 0 {
		return fail("dependency calls have missing, invalid or stale correlation IDs")
	}
	if e.Overflow != 0 || len(phase.CorrelationRequests) != expectedRequests {
		return fail("per-request correlation capacity exceeded or incoming request evidence missing")
	}
	runID := ""
	if len(phase.CorrelationRequests) > 0 {
		runID = stringsRunID(phase.CorrelationRequests[0].ID)
	}
	if !testCorrelationRunPattern.MatchString(runID) {
		return fail("registered correlation namespace missing")
	}
	histogram := map[int64]int{}
	seen := map[string]bool{}
	var total int64
	for _, sample := range phase.CorrelationRequests {
		slot, valid := chaos.RequestCorrelationSlot(runID, rule, sample.ID)
		if !valid || seen[sample.ID] || sample.Expected == nil {
			return fail("incoming request correlation or completion receipt missing")
		}
		seen[sample.ID] = true
		count := counts[chaos.CorrelationCounterID(rule, slot)]
		if count < 0 || count != *sample.Expected || count > math.MaxInt64-total {
			return fail("gateway attempts disagree with application completion receipts")
		}
		total += count
		e.Correlated++
		histogram[count]++
		if count > e.MaxAttempts {
			e.MaxAttempts = count
		}
		if count > e.Limit {
			e.ViolationCount++
			if len(e.Violations) < 100 {
				e.Violations = append(e.Violations, testRequestAttemptViolation{RequestID: sample.ID, Attempts: count})
			}
		}
	}
	if total != support {
		return fail("unassigned dependency correlation evidence present")
	}
	keys := make([]int64, 0, len(histogram))
	for count := range histogram {
		keys = append(keys, count)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, count := range keys {
		e.Distribution = append(e.Distribution, testRequestAttemptBucket{Attempts: count, Requests: histogram[count]})
	}
	if e.ViolationCount > 0 {
		return fail(fmt.Sprintf("%d requests exceed the per-request limit %d (maximum %d)", e.ViolationCount, e.Limit, e.MaxAttempts))
	}
	e.Passed = true
	return e
}

func stringsRunID(value string) string {
	for i, c := range value {
		if c == '.' {
			return value[:i]
		}
	}
	return ""
}
