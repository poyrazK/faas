package main

import (
	"fmt"
	"html/template"
	"math"
	"path/filepath"
	"strings"
)

type testRecoveryTrendConfig struct {
	MaxRecoveryIncreasePercent *float64 `yaml:"max_recovery_increase_percent" json:"max_recovery_increase_percent,omitempty"`
	MaxP95IncreasePercent      *float64 `yaml:"max_p95_increase_percent" json:"max_p95_increase_percent,omitempty"`
	MaxErrorRateIncrease       *float64 `yaml:"max_error_rate_increase" json:"max_error_rate_increase,omitempty"`
	MinSamples                 int      `yaml:"min_samples" json:"min_samples"`
}
type testRecoveryTrendValue struct {
	Choice     int      `json:"choice"`
	Request    string   `json:"request,omitempty"`
	Target     string   `json:"target"`
	Status     string   `json:"status"`
	Error      string   `json:"error,omitempty"`
	Events     int      `json:"events"`
	RecoveryMS *float64 `json:"recovery_ms,omitempty"`
	P95MS      *float64 `json:"p95_ms,omitempty"`
	ErrorRate  *float64 `json:"error_rate,omitempty"`
	References []string `json:"references,omitempty"`
}
type testRecoveryTrendPoint struct {
	Label, RunID, ReportSHA256, Anchor string
	Values                             []testRecoveryTrendValue
}
type testRecoveryTrendChartPoint struct {
	PreviousX             int
	PreviousY             *float64
	X                     int
	Y                     *float64
	Value                 *float64
	Anchor, Label, Status string
}
type testRecoveryTrendChart struct {
	Title, Unit string
	Maximum     float64
	Points      []testRecoveryTrendChartPoint
}
type testRecoveryTrend struct {
	Scenario, Profile string
	Policy            testRecoveryTrendConfig
	Points            []testRecoveryTrendPoint
	Charts            []testRecoveryTrendChart
}

func validateTestRecoveryTrendConfig(c testRecoveryTrendConfig) error {
	if c.MinSamples < 1 || c.MinSamples > testLoadMaxRequests {
		return fmt.Errorf("recovery min_samples must be from 1 to %d", testLoadMaxRequests)
	}
	for _, limit := range []*float64{c.MaxRecoveryIncreasePercent, c.MaxP95IncreasePercent} {
		if limit != nil && (!finiteNonnegative(*limit) || *limit > 1000) {
			return fmt.Errorf("recovery and p95 increase tolerances must be finite from 0 to 1000 percent")
		}
	}
	if c.MaxErrorRateIncrease != nil && (!finiteNonnegative(*c.MaxErrorRateIncrease) || *c.MaxErrorRateIncrease > 1) {
		return fmt.Errorf("error rate increase must be finite from 0 to 1")
	}
	return nil
}

func buildTestRecoveryTrend(s testScenario, coverage *testCampaignTrend, dir string, config testCampaignTrendConfig, output string) (*testRecoveryTrend, error) {
	policy := testRecoveryTrendConfig{MinSamples: 1}
	if config.Recovery != nil {
		policy = *config.Recovery
	}
	if err := validateTestRecoveryTrendConfig(policy); err != nil {
		return nil, err
	}
	result := &testRecoveryTrend{Scenario: coverage.Scenario, Profile: coverage.Profile, Policy: policy}
	for i, input := range config.Reports {
		path := input.FromReport
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		receipts, digest, err := readTestBaselineReport(path, []string{output})
		if err != nil {
			return nil, err
		}
		if digest != coverage.Points[i].ReportSHA256 {
			return nil, fmt.Errorf("report changed after coverage validation")
		}
		var root *testRunReceipt
		for j := range receipts {
			if receipts[j].RunID == input.RunID {
				root = &receipts[j]
			}
		}
		if root == nil {
			return nil, fmt.Errorf("missing campaign root")
		}
		values, err := collectTestRecoveryTrendValues(s, root, policy.MinSamples)
		if err != nil {
			return nil, err
		}
		identity := coverage.Points[i]
		point := testRecoveryTrendPoint{Label: identity.Label, RunID: identity.RunID, ReportSHA256: digest, Anchor: identity.Anchor, Values: values}
		if i > 0 {
			for j := range point.Values {
				compareTestRecoveryTrendValue(&point.Values[j], result.Points[0].Values[j], policy)
			}
		}
		result.Points = append(result.Points, point)
	}
	for j, base := range result.Points[0].Values {
		for metric := 0; metric < 3; metric++ {
			chart := testRecoveryTrendChart{Title: fmt.Sprintf("Choice %d (%s), %s: %s", base.Choice, coverage.ChoiceLabels[base.Choice], base.Target, []string{"worst recovery time", "worst phase p95 latency", "worst phase error rate"}[metric]), Unit: []string{"ms", "ms", "rate"}[metric]}
			for i, point := range result.Points {
				value := recoveryTrendMetric(point.Values[j], metric)
				if value != nil {
					chart.Maximum = math.Max(chart.Maximum, *value)
				}
				chart.Points = append(chart.Points, testRecoveryTrendChartPoint{X: 60 + 70*i, Value: value, Anchor: point.Anchor, Label: point.Label, Status: point.Values[j].Status})
			}
			if chart.Maximum == 0 {
				chart.Maximum = 1
			}
			for i := range chart.Points {
				p := &chart.Points[i]
				if p.Value != nil {
					y := 220 - 180*(*p.Value/chart.Maximum)
					p.Y = &y
					if i > 0 && chart.Points[i-1].Y != nil {
						p.PreviousX = chart.Points[i-1].X
						p.PreviousY = chart.Points[i-1].Y
					}
				}
			}
			result.Charts = append(result.Charts, chart)
		}
	}
	return result, nil
}

func recoveryTrendMetric(v testRecoveryTrendValue, metric int) *float64 {
	return []*float64{v.RecoveryMS, v.P95MS, v.ErrorRate}[metric]
}
func failTestRecoveryTrendValue(v *testRecoveryTrendValue, message string) {
	v.Status = "failed"
	if v.Error != "" {
		v.Error += "; "
	}
	v.Error += message
}
func compareTestRecoveryTrendValue(now *testRecoveryTrendValue, base testRecoveryTrendValue, policy testRecoveryTrendConfig) {
	if now.Events == 0 && now.Status == "not_planned" {
		return
	}
	for metric, allowance := range []*float64{policy.MaxRecoveryIncreasePercent, policy.MaxP95IncreasePercent, policy.MaxErrorRateIncrease} {
		if allowance == nil {
			continue
		}
		b, c := recoveryTrendMetric(base, metric), recoveryTrendMetric(*now, metric)
		label := []string{"recovery time", "p95 latency", "error rate"}[metric]
		if b == nil || c == nil {
			failTestRecoveryTrendValue(now, label+" comparison unavailable")
			continue
		}
		limit := *b + *allowance
		if metric < 2 {
			limit = *b * (1 + *allowance/100)
		}
		if *c > limit+1e-12*math.Max(1, math.Abs(limit)) {
			failTestRecoveryTrendValue(now, fmt.Sprintf("%s %.6g exceeds first-point limit %.6g", label, *c, limit))
		}
	}
}

// All planned events contribute. Missing exercise or measurements invalidate
// the affected metric instead of silently selecting only healthy recoveries.
func collectTestRecoveryTrendValues(s testScenario, root *testRunReceipt, minSamples int) ([]testRecoveryTrendValue, error) {
	spec := s.Resilience.CampaignCoverage
	var load *testLoadSpec
	for _, step := range s.Steps {
		if step.Name == spec.Step {
			load = step.Load
		}
	}
	if load == nil || len(load.ChaosSchedule) < 3 {
		return nil, fmt.Errorf("missing expanded campaign")
	}
	targets := map[string]*testLoadRecoverySpec{}
	if load.ChaosSchedule[2].Recovery != nil {
		targets[""] = load.ChaosSchedule[2].Recovery
	}
	for name, budget := range load.ChaosSchedule[2].StepRecovery {
		targets[name] = budget
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("recovery trend requires configured recovery assertions")
	}
	names := testEndpointRecoveryNames(targets)
	values := make([]testRecoveryTrendValue, 0, len(load.FaultCampaign.Choices)*len(names))
	for choice := range load.FaultCampaign.Choices {
		for _, name := range names {
			target := name
			if target == "" {
				target = "aggregate"
			}
			values = append(values, testRecoveryTrendValue{Choice: choice, Request: name, Target: target, Status: "passed"})
		}
	}
	invalid := make([][3]bool, len(values))
	for _, group := range root.FaultMatrix.Cases {
		for _, leaf := range group.Resilience.History {
			state := leaf.CampaignSeed.Seed
			for cycle := 1; cycle <= load.FaultCampaign.Count; cycle++ {
				choice := testFaultCampaignChoiceIndex(load.FaultCampaign, &state, cycle-1)
				_ = testFaultCampaignDraw(&state)
				fault := uniqueTestCampaignPhase(leaf, spec.Step, load.ChaosSchedule[2*cycle-1].Name)
				phase := uniqueTestCampaignPhase(leaf, spec.Step, load.ChaosSchedule[2*cycle].Name)
				exercised := fault != nil && fault.Chaos != nil && fault.Chaos.RulesInstalled == 1 && len(fault.Chaos.Matches) == 1 && fault.Chaos.Matches[0].Matches > 0
				for targetIndex, name := range names {
					index := choice*len(names) + targetIndex
					value := &values[index]
					value.Events++
					value.References = append(value.References, fmt.Sprintf("%s / %s / cycle %d", leaf.RunID, load.ChaosSchedule[2*cycle].Name, cycle))
					if !exercised || phase == nil || phase.Cycle != cycle || phase.StartedAt.IsZero() || !phase.FinishedAt.After(phase.StartedAt) {
						invalid[index] = [3]bool{true, true, true}
						failTestRecoveryTrendValue(value, "fault not exercised or recovery phase incomplete")
						continue
					}
					recovery := phase.Recovery
					metrics := &phase.testLoadMetrics
					budget := load.ChaosSchedule[2*cycle].Recovery
					if name != "" {
						recovery = phase.StepRecovery[name]
						budget = load.ChaosSchedule[2*cycle].StepRecovery[name]
						metrics = nil
						for j := range phase.Steps {
							if phase.Steps[j].Name == name {
								if metrics != nil {
									return nil, fmt.Errorf("duplicate endpoint metrics")
								}
								metrics = &phase.Steps[j].testLoadMetrics
							}
						}
					}
					measured, message := testOutageConfirmedRecovery(recovery, resolveTestLoadRecovery(budget))
					if message != "" {
						invalid[index][0] = true
						failTestRecoveryTrendValue(value, message)
					} else {
						maxTestRecoveryTrendMetric(&value.RecoveryMS, *measured)
					}
					if metrics == nil || metrics.Requests < minSamples || metrics.Requests > testLoadMaxRequests || metrics.Failures < 0 || metrics.Failures > metrics.Requests || !finiteNonnegative(metrics.ErrorRate) || metrics.ErrorRate > 1 || math.Abs(metrics.ErrorRate-float64(metrics.Failures)/float64(metrics.Requests)) > 1e-9 {
						invalid[index][1], invalid[index][2] = true, true
						failTestRecoveryTrendValue(value, "missing or inconsistent recovery phase samples")
						continue
					}
					maxTestRecoveryTrendMetric(&value.ErrorRate, metrics.ErrorRate)
					if metrics.Latency.Samples < minSamples || metrics.Latency.Samples > metrics.Requests || !finiteNonnegative(metrics.Latency.P95MS) {
						invalid[index][1] = true
						failTestRecoveryTrendValue(value, "missing or invalid recovery p95 samples")
					} else {
						maxTestRecoveryTrendMetric(&value.P95MS, metrics.Latency.P95MS)
					}
				}
			}
		}
	}
	for i := range values {
		value := &values[i]
		if value.Events == 0 {
			value.Status = "not_planned"
			continue
		}
		if root.FaultMatrix.CampaignCoverage.Choices[value.Choice].Exercised != root.FaultMatrix.CampaignCoverage.Choices[value.Choice].Planned {
			invalid[i] = [3]bool{true, true, true}
			failTestRecoveryTrendValue(value, "not every planned fault event was exercised")
		}
		if invalid[i][0] {
			value.RecoveryMS = nil
		}
		if invalid[i][1] {
			value.P95MS = nil
		}
		if invalid[i][2] {
			value.ErrorRate = nil
		}
	}
	return values, nil
}
func maxTestRecoveryTrendMetric(target **float64, value float64) {
	if *target == nil || value > **target {
		v := value
		*target = &v
	}
}

var recoveryTrendTemplate = template.Must(template.New("recovery-trend").Funcs(template.FuncMap{
	"number": func(v *float64) string {
		if v == nil {
			return "unavailable"
		}
		return fmt.Sprintf("%.6g", *v)
	},
	"join": strings.Join,
}).Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Recovery performance trend</title><style>body{font:16px system-ui;margin:2rem;max-width:1100px}table{border-collapse:collapse;width:100%}td,th{padding:.5rem;border:1px solid #ccc;text-align:left}svg{width:100%;max-width:800px}.failed{color:#b42318}code{overflow-wrap:anywhere}</style><h1>Recovery performance trend</h1><p>{{.Scenario}} / {{.Profile}}. Input order is explicit. All tolerances compare with the first point. Each metric is the maximum across all planned events for that choice and target. Phase p95 values are not pooled percentiles. Missing exercise, unconfirmed recovery or insufficient samples produce gaps, not zeroes. These descriptive trends do not establish statistical confidence.</p><p>Minimum samples per recovery phase: {{.Policy.MinSamples}}. Maximum recovery increase (%): {{number .Policy.MaxRecoveryIncreasePercent}}; maximum p95 increase (%): {{number .Policy.MaxP95IncreasePercent}}; maximum error-rate increase: {{number .Policy.MaxErrorRateIncrease}}.</p>
{{range .Charts}}<h2>{{.Title}}</h2><svg viewBox="0 0 760 260" role="img" aria-label="{{.Title}}"><path d="M60 40V220H730" fill="none" stroke="#777"/><text x="0" y="30">{{.Maximum}} {{.Unit}}</text><text x="15" y="220">0</text>{{$points:=.Points}}{{range $i,$p:=.Points}}{{if $p.PreviousY}}<line x1="{{$p.PreviousX}}" y1="{{$p.PreviousY}}" x2="{{$p.X}}" y2="{{$p.Y}}" stroke="#1463bb"/>{{end}}{{if $p.Value}}<a href="#{{$p.Anchor}}"><circle cx="{{$p.X}}" cy="{{$p.Y}}" r="6" fill="{{if eq $p.Status "failed"}}#b42318{{else}}#1463bb{{end}}"><title>{{$p.Label}}: {{number $p.Value}}; {{$p.Status}}</title></circle></a>{{end}}<text x="{{$p.X}}" y="245">{{$p.Label}}</text>{{end}}</svg>{{end}}
{{range .Points}}<section id="{{.Anchor}}"><h2>{{.Label}}</h2><p>Run ID: <code>{{.RunID}}</code><br>Report SHA256: <code>{{.ReportSHA256}}</code></p><table><tr><th>Fault choice / target</th><th>Events</th><th>Worst recovery (ms)</th><th>Worst phase p95 (ms)</th><th>Worst phase error rate</th><th>Status / findings</th><th>Leaf runs / recovery phases</th></tr>{{range .Values}}<tr><td>{{.Choice}} / {{.Target}}</td><td>{{.Events}}</td><td>{{number .RecoveryMS}}</td><td>{{number .P95MS}}</td><td>{{number .ErrorRate}}</td><td class="{{.Status}}">{{.Status}}: {{.Error}}</td><td>{{join .References "; "}}</td></tr>{{end}}</table></section>{{end}}</html>`))

func recoveryTrendNumber(v *float64) string {
	if v == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.6g", *v)
}
