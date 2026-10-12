package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const testFailureDiagnosticMaxBytes = 256 * 1024
const testFailureDiagnosticMaxWorkloads = 8

type testRecoveryFailureDiagnostics struct {
	ID         string                                `json:"id"`
	StartedAt  time.Time                             `json:"started_at"`
	FinishedAt time.Time                             `json:"finished_at"`
	Assertions []testRecoveryDiagnosticAssertion     `json:"assertions"`
	Workloads  map[string]testRecoveryDiagnosticLogs `json:"workloads,omitempty"`
	Truncated  bool                                  `json:"truncated,omitempty"`
}
type testRecoveryDiagnosticAssertion struct {
	Step     string                        `json:"step"`
	Phase    string                        `json:"phase"`
	Request  string                        `json:"request,omitempty"`
	Requests int                           `json:"requests"`
	Failures int                           `json:"failures"`
	P95MS    float64                       `json:"p95_ms"`
	Faults   []testRecoveryDiagnosticFault `json:"faults,omitempty"`
}
type testRecoveryDiagnosticFault struct {
	From    string `json:"from,omitempty"`
	To      string `json:"to"`
	Kind    string `json:"kind"`
	Port    int    `json:"port,omitempty"`
	Matches int64  `json:"matches"`
}
type testRecoveryDiagnosticLogs struct {
	Logs            []api.LogEvent `json:"logs,omitempty"`
	Truncated       bool           `json:"truncated,omitempty"`
	CollectionError string         `json:"collection_error,omitempty"`
}
type testDiagnosticLogsClient interface {
	StreamAppLogs(context.Context, string, string, bool, api.LogFilter) (io.ReadCloser, error)
}

var testDiagnosticSensitiveLine = regexp.MustCompile(`(?i)authorization|password|passwd|secret|token|api[_-]?key|cookie|private[ _-]?key|\bbody\b|\bpayload\b|\bheaders?\b`)
var testDiagnosticOpaqueValue = regexp.MustCompile(`[A-Za-z0-9_+/=]{32,}`)

func redactTestDiagnosticText(value string, secrets []string, limit int) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		value = strings.ReplaceAll(value, secret, "[redacted]")
		value = strings.ReplaceAll(value, url.QueryEscape(secret), "[redacted]")
	}
	if testDiagnosticSensitiveLine.MatchString(value) {
		value = "[redacted sensitive log line]"
	}
	value = testDiagnosticOpaqueValue.ReplaceAllString(value, "[redacted]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) && len(value) > 0 {
			value = value[:len(value)-1]
		}
	}
	return strings.ToValidUTF8(value, "")
}

// References point to assertions and fault evidence in the same receipt. Only
// counts and latency are copied: request headers, bodies, and error text are excluded.
func prepareTestRecoveryFailureDiagnostics(receipt *testRunReceipt) *testRecoveryFailureDiagnostics {
	var bundle *testRecoveryFailureDiagnostics
	for _, step := range receipt.Steps {
		if step.Load == nil {
			continue
		}
		for i, phase := range step.Load.ChaosPhases {
			for _, name := range testEndpointRecoveryNames(testPhaseRecoveryEvidence(phase)) {
				r := testPhaseRecoveryEvidence(phase)[name]
				if r == nil || r.Passed {
					continue
				}
				if bundle == nil {
					digest := sha256.Sum256([]byte(receipt.RunID + receipt.StartedAt.String()))
					bundle = &testRecoveryFailureDiagnostics{ID: "recovery-diagnostics-" + hex.EncodeToString(digest[:8]), Workloads: map[string]testRecoveryDiagnosticLogs{}}
				}
				r.DiagnosticsRef = bundle.ID
				if len(bundle.Assertions) >= 20 {
					bundle.Truncated = true
					continue
				}
				metrics := phase.testLoadMetrics
				if name != "" {
					metrics = testLoadMetrics{}
					for _, endpoint := range phase.Steps {
						if endpoint.Name == name {
							metrics = endpoint.testLoadMetrics
							break
						}
					}
				}
				assertion := testRecoveryDiagnosticAssertion{Step: step.Name, Phase: phase.Name, Request: name, Requests: metrics.Requests, Failures: metrics.Failures, P95MS: metrics.Latency.P95MS}
				if i > 0 && step.Load.ChaosPhases[i-1].Chaos != nil {
					for _, match := range step.Load.ChaosPhases[i-1].Chaos.Matches {
						if len(assertion.Faults) >= 20 {
							bundle.Truncated = true
							break
						}
						assertion.Faults = append(assertion.Faults, testRecoveryDiagnosticFault{From: match.Rule.From, To: match.Rule.To, Kind: match.Rule.Kind, Port: match.Rule.Port, Matches: match.Matches})
					}
				}
				bundle.Assertions = append(bundle.Assertions, assertion)
				deadline := phase.StartedAt.Add(time.Duration(r.WithinMS) * time.Millisecond)
				if r.Stability != nil && r.Stability.RelapseMS != nil {
					deadline = phase.StartedAt.Add(time.Duration(*r.Stability.RelapseMS * float64(time.Millisecond)))
				}
				start := deadline.Add(-5 * time.Second)
				if !phase.StartedAt.IsZero() && (bundle.StartedAt.IsZero() || start.Before(bundle.StartedAt)) {
					bundle.StartedAt = start
				}
				end := deadline.Add(5 * time.Second)
				if !phase.FinishedAt.IsZero() && phase.FinishedAt.Before(end) {
					end = phase.FinishedAt
				}
				if end.After(bundle.FinishedAt) {
					bundle.FinishedAt = end
				}
			}
		}
	}
	if bundle != nil {
		now := time.Now().UTC()
		if bundle.StartedAt.IsZero() {
			bundle.StartedAt = now.Add(-time.Minute)
		}
		if bundle.FinishedAt.IsZero() || bundle.FinishedAt.After(now) || bundle.FinishedAt.Before(bundle.StartedAt) {
			bundle.FinishedAt = now
		}
		if bundle.StartedAt.After(bundle.FinishedAt) {
			bundle.StartedAt = bundle.FinishedAt.Add(-5 * time.Second)
		}
	}
	return bundle
}

// Read a finite SSE page synchronously through a byte-limited scanner. Unknown
// events are excluded; gaps and unavailable streams are recorded without raw errors.
func collectTestRecoveryDiagnosticLogs(ctx context.Context, client testDiagnosticLogsClient, slug string, bundle *testRecoveryFailureDiagnostics, secrets []string) (result testRecoveryDiagnosticLogs) {
	body, err := client.StreamAppLogs(ctx, slug, "", false, api.LogFilter{Since: bundle.StartedAt.Format(time.RFC3339Nano)})
	if err != nil {
		result.CollectionError = "application logs unavailable"
		return
	}
	defer func() { _ = body.Close() }()
	stopClose := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopClose()
	reader := io.LimitReader(body, 64*1024+1)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 64*1024+1)
	event, data := "", ""
	used := 0
	wire := 0
	flush := func() {
		if event == "gap" {
			result.CollectionError = "application log history has gaps"
		}
		if event == "degraded" || event == "error" {
			result.CollectionError = "application logs unavailable"
		}
		if event != "log" && event != "" {
			return
		}
		var row api.LogEvent
		if json.Unmarshal([]byte(data), &row) != nil || row.Line == "" {
			return
		}
		at, err := time.Parse(time.RFC3339Nano, row.WrittenAt)
		if err != nil || at.Before(bundle.StartedAt) || at.After(bundle.FinishedAt) {
			return
		}
		row.Line = redactTestDiagnosticText(row.Line, secrets, 1024)
		row.InstanceID = redactTestDiagnosticText(row.InstanceID, secrets, 128)
		row.Stream = redactTestDiagnosticText(row.Stream, secrets, 16)
		row.Level = redactTestDiagnosticText(row.Level, secrets, 16)
		encoded, _ := json.Marshal(row)
		if len(result.Logs) >= 20 || used+len(encoded) > 16*1024 {
			result.Truncated = true
			return
		}
		used += len(encoded)
		result.Logs = append(result.Logs, row)
	}
	for scanner.Scan() {
		line := scanner.Text()
		wire += len(line) + 1
		if wire > 64*1024 {
			result.Truncated = true
			break
		}
		if line == "" {
			flush()
			event, data = "", ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			if data != "" {
				data += "\n"
			}
			data += strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		}
	}
	if data != "" && !result.Truncated {
		flush()
	}
	if scanner.Err() != nil || ctx.Err() != nil {
		result.Truncated = true
		result.CollectionError = "application log collection interrupted"
	}
	return
}

func collectTestRecoveryFailureDiagnostics(ctx context.Context, client testDiagnosticLogsClient, receipt *testRunReceipt, slugs map[string]string, secrets []string) {
	secrets = testDiagnosticRedactions(secrets)
	bundle := prepareTestRecoveryFailureDiagnostics(receipt)
	if bundle == nil {
		return
	}
	receipt.FailureDiagnostics = bundle
	for _, name := range testEndpointRecoveryNames(slugs) {
		if len(bundle.Workloads) >= testFailureDiagnosticMaxWorkloads || ctx.Err() != nil {
			bundle.Truncated = true
			break
		}
		workloadCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		bundle.Workloads[name] = collectTestRecoveryDiagnosticLogs(workloadCtx, client, slugs[name], bundle, secrets)
		cancel()
	}
	encoded, err := json.Marshal(bundle)
	if err != nil || len(encoded) > testFailureDiagnosticMaxBytes {
		bundle.Workloads = nil
		bundle.Truncated = true
	}
}

func testDiagnosticScenarioSecrets(scenario testScenario) []string {
	var values []string
	steps := append(append([]testHTTPRequest(nil), scenario.Requests...), scenario.Checks...)
	for _, step := range scenario.Steps {
		steps = append(steps, step.Requests...)
		steps = append(steps, step.Checks...)
	}
	for _, step := range steps {
		for key, value := range step.Headers {
			if testDiagnosticSensitiveLine.MatchString(key) {
				values = append(values, value)
			}
		}
	}
	return values
}

func testDiagnosticRedactions(values []string) []string {
	known := map[string]bool{}
	for _, value := range values {
		if value == "" {
			continue
		}
		known[value] = true
		parts := strings.Fields(value)
		if len(parts) == 2 && (strings.EqualFold(parts[0], "Bearer") || strings.EqualFold(parts[0], "Basic")) {
			known[parts[1]] = true
		}
		if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" && parsed.User != nil {
			if password, ok := parsed.User.Password(); ok && password != "" {
				known[password] = true
			}
		}
	}
	result := make([]string, 0, len(known)*2)
	for value := range known {
		result = append(result, value, base64.StdEncoding.EncodeToString([]byte(value)))
	}
	// Redact longest values first so overlapping credentials cannot expose suffixes.
	sort.Slice(result, func(i, j int) bool { return len(result[i]) > len(result[j]) })
	return result
}
