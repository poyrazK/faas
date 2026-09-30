package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type testHTMLReport struct {
	Title     string
	Suite     string
	Generated string
	Passed    int
	Failed    int
	Skipped   int
	Receipts  []testRunReceipt
}

const testHTMLStyles = `
:root{color-scheme:light dark;--bg:#f5f7fa;--panel:#fff;--ink:#172033;--muted:#5b6475;--line:#dce1e8;--green:#087443;--red:#b42318;--amber:#8a5700;--blue:#175cd3}
@media(prefers-color-scheme:dark){:root{--bg:#111827;--panel:#1f2937;--ink:#f3f4f6;--muted:#aab3c2;--line:#374151;--green:#6ce9a6;--red:#fda29b;--amber:#fec84b;--blue:#84adff}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.5 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}main{max-width:1120px;margin:0 auto;padding:32px 20px 64px}h1{font-size:1.8rem;margin:0 0 4px}.muted{color:var(--muted)}.summary{display:flex;gap:12px;flex-wrap:wrap;margin:24px 0}.stat,.panel{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:14px 16px}.stat{min-width:120px}.stat strong{display:block;font-size:1.45rem}.stat .label{color:var(--muted);font-size:.85rem}.run{background:var(--panel);border:1px solid var(--line);border-radius:10px;margin:12px 0;overflow:hidden}.run>summary{display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:14px 16px;cursor:pointer;list-style:none}.run>summary::-webkit-details-marker{display:none}.run>summary strong{font-size:1.05rem}.run-body{padding:0 16px 16px}.badge{font-size:.78rem;font-weight:700;text-transform:uppercase;letter-spacing:.04em;border-radius:999px;padding:3px 9px;background:var(--line)}.passed{color:var(--green)}.failed{color:var(--red)}.skipped{color:var(--amber)}.error{border-left:3px solid var(--red);padding:8px 12px;white-space:pre-wrap;overflow-wrap:anywhere}.meta{display:flex;gap:18px;flex-wrap:wrap;color:var(--muted);font-size:.9rem;margin:10px 0}h2{font-size:1.15rem;margin:28px 0 10px}h3{font-size:1rem;margin:18px 0 8px}table{width:100%;border-collapse:collapse;margin:8px 0 16px;font-size:.9rem}th,td{text-align:left;padding:7px 9px;border-bottom:1px solid var(--line);vertical-align:top}th{color:var(--muted);font-weight:600}code,pre{font: .85rem/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}code{overflow-wrap:anywhere}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:650px;overflow:auto;background:var(--bg);border:1px solid var(--line);border-radius:8px;padding:14px}.nested{margin:12px 0}.nested>summary{cursor:pointer;color:var(--blue)}.nowrap{white-space:nowrap}
`

const testHTMLReportTemplate = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>{{styles}}</style></head>
<body><main>
<h1>{{.Title}}</h1>
<div class="muted">{{if .Suite}}Suite {{.Suite}} · {{end}}Generated {{.Generated}} · {{len .Receipts}} run{{if ne (len .Receipts) 1}}s{{end}}</div>
<section class="summary" aria-label="Run summary">
  <div class="stat"><strong class="passed">{{.Passed}}</strong><span class="label">Passed</span></div>
  <div class="stat"><strong class="failed">{{.Failed}}</strong><span class="label">Failed</span></div>
  <div class="stat"><strong class="skipped">{{.Skipped}}</strong><span class="label">Skipped</span></div>
</section>
<h2>Runs</h2>
{{range .Receipts}}
<details class="run" open><summary><span class="badge {{.Status}}">{{.Status}}</span><strong>{{.Scenario}}</strong><span>{{.Profile}} · {{.Engine}}</span>{{if .Case}}<span>case {{.Case}}</span>{{end}}<span>attempt {{.Attempt}}</span><span class="muted">{{seconds .DurationMS}}s</span></summary>
<div class="run-body">
  {{if or .Error .CleanupError .SkipReason}}<div class="error failed">{{if .Error}}{{.Error}}{{end}}{{if .CleanupError}}<br>Cleanup: {{.CleanupError}}{{end}}{{if .SkipReason}}<br>{{.SkipReason}}{{end}}</div>{{end}}
  <div class="meta"><span>Started: {{.StartedAt}}</span>{{if .RunID}}<span>Run: <code>{{.RunID}}</code></span>{{end}}{{if .AppSlug}}<span>App: {{.AppSlug}}</span>{{end}}{{if .DeploymentID}}<span>Deployment: <code>{{.DeploymentID}}</code></span>{{end}}</div>
  {{if .Phases}}<h3>Execution phases</h3><table><thead><tr><th>Phase</th><th>Status</th><th>Duration</th></tr></thead><tbody>{{range .Phases}}<tr><td>{{.Name}}</td><td>{{.Status}}</td><td>{{seconds .DurationMS}}s</td></tr>{{end}}</tbody></table>{{end}}
  {{if .Requests}}<h3>HTTP steps</h3><table><thead><tr><th>Step</th><th>Request</th><th>Status</th><th>Duration</th><th>Result</th><th>Details</th></tr></thead><tbody>{{range .Requests}}<tr><td>{{.Name}}</td><td>{{.Method}} <code>{{.Path}}</code></td><td>{{if .Status}}{{.Status}}{{else}}—{{end}}</td><td>{{seconds .DurationMS}}s</td><td>{{if .Passed}}passed{{else}}failed{{end}}</td><td>{{.Error}}</td></tr>{{end}}</tbody></table>{{end}}
  {{if .Load}}<h3>Load results · {{.Load.Mode}} · {{.Load.Status}}</h3><div class="meta">{{if .Load.Arrival}}<span>{{.Load.Arrival.Scheduled}}/{{.Load.Arrival.Planned}} arrivals scheduled</span><span>target {{.Load.Arrival.TargetRate}} journeys/s</span><span>{{.Load.Arrival.Dropped}} dropped arrivals</span>{{else}}<span>{{.Load.IterationsCompleted}}/{{.Load.IterationLimit}} journeys completed</span>{{end}}<span>{{.Load.Requests}} requests</span><span>{{printf "%.2f" .Load.RequestsPerSecond}} req/s</span><span>{{printf "%.2f%%" (mul100 .Load.ErrorRate)}} errors</span></div>
  <table><thead><tr><th>Metric</th><th>Samples</th><th>Mean</th><th>p50</th><th>p95</th><th>p99</th><th>Max</th><th>Failures</th></tr></thead><tbody><tr><td>All requests</td><td>{{.Load.Latency.Samples}}</td><td>{{printf "%.2f ms" .Load.Latency.MeanMS}}</td><td>{{printf "%.2f ms" .Load.Latency.P50MS}}</td><td>{{printf "%.2f ms" .Load.Latency.P95MS}}</td><td>{{printf "%.2f ms" .Load.Latency.P99MS}}</td><td>{{printf "%.2f ms" .Load.Latency.MaxMS}}</td><td>{{.Load.Failures}}</td></tr>{{range .Load.Steps}}<tr><td>{{.Name}}</td><td>{{.Latency.Samples}}</td><td>{{printf "%.2f ms" .Latency.MeanMS}}</td><td>{{printf "%.2f ms" .Latency.P50MS}}</td><td>{{printf "%.2f ms" .Latency.P95MS}}</td><td>{{printf "%.2f ms" .Latency.P99MS}}</td><td>{{printf "%.2f ms" .Latency.MaxMS}}</td><td>{{.Failures}}</td></tr>{{end}}</tbody></table>{{end}}
  {{if .Baseline}}<h3>Performance baseline · <span class="{{.Baseline.Status}}">{{.Baseline.Status}}</span></h3>{{if .Baseline.Checks}}<table><thead><tr><th>Step</th><th>Metric</th><th>Baseline</th><th>Current</th><th>Change</th><th>Limit</th><th>Result</th></tr></thead><tbody>{{range .Baseline.Checks}}<tr><td>{{if .Step}}{{.Step}}{{else}}aggregate{{end}}</td><td>{{.Metric}}</td><td>{{printf "%.4f" .Baseline}}</td><td>{{printf "%.4f" .Current}}</td><td>{{printf "%+.4f" .Delta}}</td><td>{{printf "%.4f" .Limit}}</td><td>{{if .Passed}}passed{{else}}failed{{end}}</td></tr>{{end}}</tbody></table>{{end}}{{if .Baseline.Error}}<div class="error">{{.Baseline.Error}}</div>{{end}}{{end}}
  {{if or .Evidence .Outputs .Invocations .Deliveries .ServiceWake .ServiceHot .Diagnostics}}<details class="nested"><summary>Platform evidence</summary><pre>{{json (platformEvidence .)}}</pre></details>{{end}}
  <details class="nested"><summary>Full run evidence (JSON)</summary><pre>{{json .}}</pre></details>
</div></details>
{{end}}
</main></body></html>`

func writeTestHTMLReport(path, suite string, receipts []testRunReceipt) error {
	data := testHTMLReport{Title: "Gregale test report", Suite: suite, Generated: testReportTimestamp(), Receipts: receipts}
	for _, receipt := range receipts {
		switch receipt.Status {
		case "passed":
			data.Passed++
		case "skipped":
			data.Skipped++
		default:
			data.Failed++
		}
	}
	return renderTestHTML(path, testHTMLReportTemplate, data)
}

func renderTestHTML(path, source string, data any) error {
	funcs := template.FuncMap{
		"json": func(value any) string {
			body, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return "Could not render JSON: " + err.Error()
			}
			return string(body)
		},
		"seconds": func(milliseconds int64) string { return fmt.Sprintf("%.3f", float64(milliseconds)/1000) },
		"mul100":  func(value float64) float64 { return value * 100 },
		"delta":   func(metric testMetricDelta) string { return formatTestMetricDelta(metric) },
		"metricValue": func(metric testMetricDelta, before bool) string {
			if before && metric.Before != nil {
				return formatTestMetricValue(*metric.Before, metric.Unit)
			}
			if !before && metric.After != nil {
				return formatTestMetricValue(*metric.After, metric.Unit)
			}
			return "—"
		},
		"platformEvidence": func(receipt testRunReceipt) any {
			return struct {
				Evidence    any  `json:"evidence,omitempty"`
				Outputs     any  `json:"outputs,omitempty"`
				Invocations any  `json:"invocations,omitempty"`
				Deliveries  any  `json:"deliveries,omitempty"`
				ServiceWake any  `json:"service_wake,omitempty"`
				ServiceHot  any  `json:"service_hot,omitempty"`
				Diagnostics any  `json:"diagnostics,omitempty"`
				LocalApp    any  `json:"local_app,omitempty"`
				QueueIdle   bool `json:"queue_idle,omitempty"`
			}{receipt.Evidence, receipt.Outputs, receipt.Invocations, receipt.Deliveries, receipt.ServiceWake, receipt.ServiceHot, receipt.Diagnostics, receipt.LocalApp, receipt.QueueIdle}
		},
	}
	// The stylesheet is a compile-time constant. Insert it before parsing the
	// HTML template so no runtime value bypasses html/template's escaping.
	source = strings.ReplaceAll(source, "{{styles}}", testHTMLStyles)
	parsed, err := template.New("gregale-test-report").Funcs(funcs).Parse(source)
	if err != nil {
		return fmt.Errorf("parse HTML report: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create HTML report: %w", err)
	}
	if err := parsed.Execute(file, data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write HTML report: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close HTML report: %w", err)
	}
	return nil
}

func formatTestMetricValue(value float64, unit string) string {
	if unit == "" {
		return fmt.Sprintf("%.3f", value)
	}
	return fmt.Sprintf("%.3f %s", value, unit)
}

func testReportTimestamp() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
}

// validateDistinctTestReportPaths prevents one requested output from silently
// replacing another report, including when an existing symlink or hard link is
// used to spell the same destination.
func validateDistinctTestReportPaths(paths ...string) error {
	seen := make(map[string]string, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		absolute, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			return fmt.Errorf("resolve report path %q: %w", path, err)
		}
		identity := absolute
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			identity = resolved
		} else if resolvedDir, dirErr := filepath.EvalSymlinks(filepath.Dir(absolute)); dirErr == nil {
			identity = filepath.Join(resolvedDir, filepath.Base(absolute))
		}
		if previous, ok := seen[identity]; ok {
			return fmt.Errorf("report outputs %q and %q refer to the same file", previous, path)
		}
		seen[identity] = path
		for _, previous := range paths {
			if previous == path || previous == "" {
				continue
			}
			previousInfo, previousErr := os.Stat(previous)
			currentInfo, currentErr := os.Stat(path)
			if previousErr == nil && currentErr == nil && os.SameFile(previousInfo, currentInfo) {
				return errors.New("report outputs must be different files")
			}
		}
	}
	return nil
}

func testReportPathAliases(path, other string) (bool, error) {
	if path == "" || other == "" {
		return false, nil
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false, err
	}
	absOther, err := filepath.Abs(filepath.Clean(other))
	if err != nil {
		return false, err
	}
	resolve := func(value string) string {
		if resolved, err := filepath.EvalSymlinks(value); err == nil {
			return resolved
		}
		if directory, err := filepath.EvalSymlinks(filepath.Dir(value)); err == nil {
			return filepath.Join(directory, filepath.Base(value))
		}
		return value
	}
	if resolve(absPath) == resolve(absOther) {
		return true, nil
	}
	pathInfo, pathErr := os.Stat(path)
	otherInfo, otherErr := os.Stat(other)
	if pathErr == nil && otherErr == nil {
		return os.SameFile(pathInfo, otherInfo), nil
	}
	if pathErr != nil && !os.IsNotExist(pathErr) {
		return false, pathErr
	}
	if otherErr != nil && !os.IsNotExist(otherErr) {
		return false, otherErr
	}
	return false, nil
}
