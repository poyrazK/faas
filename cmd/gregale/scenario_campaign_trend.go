package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"gopkg.in/yaml.v3"
)

type testCampaignTrendInput struct {
	FromReport string `yaml:"from_report"`
	RunID      string `yaml:"run_id"`
	Label      string `yaml:"label"`
}
type testCampaignTrendConfig struct {
	Recovery                *testRecoveryTrendConfig `yaml:"recovery,omitempty"`
	Reports                 []testCampaignTrendInput `yaml:"reports"`
	MinExercisedRate        float64                  `yaml:"min_exercised_rate"`
	MaxDropPercentagePoints map[int]float64          `yaml:"max_drop_percentage_points"`
	MinPlannedEvents        int                      `yaml:"min_planned_events"`
}
type testCampaignTrendPoint struct {
	Label, RunID, ReportSHA256, Anchor string
	Checks                             []testCampaignCoverageRegressionChoice
}
type testCampaignTrend struct {
	Scenario, Profile string
	ChoiceLabels      []string
	Points            []testCampaignTrendPoint
}

// Input order is explicit; every point is recomputed from complete raw history.
func buildTestCampaignTrend(s testScenario, name, configDir string, config testCampaignTrendConfig, output string) (*testCampaignTrend, error) {
	if s.Resilience == nil || s.Resilience.CampaignCoverage == nil {
		return nil, fmt.Errorf("trend requires a campaign coverage scenario")
	}
	if len(config.Reports) < 2 || len(config.Reports) > 10 {
		return nil, fmt.Errorf("trend requires 2 to 10 ordered reports")
	}
	if !finiteNonnegative(config.MinExercisedRate) || config.MinExercisedRate > 1 {
		return nil, fmt.Errorf("min_exercised_rate must be finite from 0 to 1")
	}
	if len(config.MaxDropPercentagePoints) == 0 && config.MinPlannedEvents != 0 {
		return nil, fmt.Errorf("min_planned_events requires drop allowances")
	}
	if len(config.MaxDropPercentagePoints) > 0 && (config.MinPlannedEvents < 2 || config.MinPlannedEvents > 180) {
		return nil, fmt.Errorf("min_planned_events must be from 2 to 180")
	}
	trend := &testCampaignTrend{Scenario: name}
	for _, step := range s.Steps {
		if step.Name == s.Resilience.CampaignCoverage.Step && step.Load != nil && step.Load.FaultCampaign != nil {
			for _, choice := range step.Load.FaultCampaign.Choices {
				trend.ChoiceLabels = append(trend.ChoiceLabels, fmt.Sprintf("%s (%s)", choice.Rule.Kind, choice.Duration))
			}
		}
	}
	var baseline *testCampaignCoverageGuidance
	seen := map[string]bool{}
	// Reserve roots before checking leaves, including roots in later inputs.
	for _, input := range config.Reports {
		if input.FromReport == "" || input.RunID == "" || seen[input.RunID] {
			return nil, fmt.Errorf("reports require distinct run IDs and paths")
		}
		seen[input.RunID] = true
	}
	for n, input := range config.Reports {
		path := input.FromReport
		if !filepath.IsAbs(path) {
			path = filepath.Join(configDir, path)
		}
		batch, err := readTestCampaignGuidanceBatch(s, testCampaignGuidanceInput{FromReport: path, RunID: input.RunID}, []string{output})
		if err != nil {
			return nil, fmt.Errorf("trend point %d: %w", n+1, err)
		}
		if batch.Scenario != name {
			return nil, fmt.Errorf("trend scenario mismatch")
		}
		for _, id := range batch.leafRunIDs {
			if seen[id] {
				return nil, fmt.Errorf("trend contains overlapping run history")
			}
			seen[id] = true
		}
		if baseline == nil {
			baseline = batch
			trend.Profile = batch.Profile
		}
		if batch.Profile != baseline.Profile || batch.runsPerSeed != baseline.runsPerSeed || !reflect.DeepEqual(batch.ExcludedSeeds, baseline.ExcludedSeeds) || !reflect.DeepEqual(batch.planned, baseline.planned) {
			return nil, fmt.Errorf("trend requires identical profile, ordered seeds, repetitions and planned counts")
		}
		for index, allowance := range config.MaxDropPercentagePoints {
			if index < 0 || index >= len(batch.planned) || !finiteNonnegative(allowance) || allowance > 100 || batch.planned[index] < config.MinPlannedEvents {
				return nil, fmt.Errorf("invalid allowance or insufficient planned events for choice %d", index)
			}
		}
		r := *s.Resilience
		c := *r.CampaignCoverage
		c.Regression = &testCampaignCoverageRegressionSpec{RunID: baseline.RunID, MinExercisedRate: config.MinExercisedRate, MaxDropPercentagePoints: config.MaxDropPercentagePoints, MinPlannedEvents: config.MinPlannedEvents, Baseline: &testCampaignCoverageRegressionBaseline{ReportSHA256: baseline.ReportSHA256, Planned: baseline.planned, Exercised: baseline.Observed}}
		r.CampaignCoverage = &c
		variant := s
		variant.Resilience = &r
		evidence := &testCampaignCoverageEvidence{}
		for i, p := range batch.planned {
			evidence.Choices = append(evidence.Choices, testCampaignChoiceCoverage{Index: i, Planned: p, Exercised: batch.Observed[i]})
		}
		// Breaches are report findings, not malformed input failures.
		_ = evaluateTestCampaignCoverageRegression(variant, evidence)
		label := input.Label
		if label == "" {
			label = input.RunID
		}
		trend.Points = append(trend.Points, testCampaignTrendPoint{Label: label, RunID: input.RunID, ReportSHA256: batch.ReportSHA256, Anchor: fmt.Sprintf("point-%d", n+1), Checks: evidence.Regression.Choices})
	}
	return trend, nil
}

func cmdTestCoverageTrend(args []string) int { return cmdTestTrend(args, false) }
func cmdTestRecoveryTrend(args []string) int { return cmdTestTrend(args, true) }
func cmdTestTrend(args []string, recovery bool) int {
	command := "coverage-trend"
	if recovery {
		command = "recovery-trend"
	}
	fs := newFlagSet("test "+command, flag.ContinueOnError)
	manifest := fs.String("manifest", "gregale-test.yaml", "scenario manifest")
	scenario := fs.String("scenario", "", "campaign scenario name")
	configPath := fs.String("config", "", "ordered trend inputs and tolerances YAML")
	htmlPath := fs.String("html", "", "standalone HTML report output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *scenario == "" || *configPath == "" || *htmlPath == "" {
		return printErr("Invalid trend options", fmt.Errorf("--scenario, --config and --html are required"))
	}
	data, err := os.ReadFile(*configPath)
	if err != nil {
		return printErr("Read trend configuration", err)
	}
	var config testCampaignTrendConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err = decoder.Decode(&config); err != nil {
		return printErr("Invalid trend configuration", err)
	}
	var extra interface{}
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return printErr("Invalid trend configuration", fmt.Errorf("expected one YAML document"))
	}
	scenarios, _, err := readTestManifestForScenario(*manifest, *scenario, nil)
	if err != nil {
		return printErr("Read trend manifest", err)
	}
	s, ok := scenarios[*scenario]
	if !ok {
		return printErr("Invalid trend scenario", fmt.Errorf("unknown scenario %s", *scenario))
	}
	// Protect the configuration and manifest as well as report inputs.
	for _, path := range []string{*configPath, *manifest} {
		a, e := os.Stat(path)
		if e != nil {
			return printErr("Read trend input", e)
		}
		b, e := os.Stat(*htmlPath)
		if e == nil && os.SameFile(a, b) {
			return printErr("Invalid trend output", fmt.Errorf("output aliases input"))
		}
	}
	trend, err := buildTestCampaignTrend(s, *scenario, filepath.Dir(*configPath), config, *htmlPath)
	if err != nil {
		return printErr("Invalid coverage trend", err)
	}
	var report bytes.Buffer
	if recovery {
		result, buildErr := buildTestRecoveryTrend(s, trend, filepath.Dir(*configPath), config, *htmlPath)
		if buildErr != nil {
			return printErr("Invalid recovery trend", buildErr)
		}
		err = recoveryTrendTemplate.Execute(&report, result)
	} else {
		if config.Recovery != nil {
			return printErr("Invalid coverage trend", fmt.Errorf("recovery settings require test recovery-trend"))
		}
		err = campaignTrendTemplate.Execute(&report, trend)
	}
	if err != nil {
		return printErr("Render coverage trend", err)
	}
	if err = os.WriteFile(*htmlPath, report.Bytes(), 0600); err != nil {
		return printErr("Write coverage trend", err)
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %d ordered points written to %s\n", command, len(trend.Points), *htmlPath)
	return 0
}

var campaignTrendTemplate = template.Must(template.New("trend").Funcs(template.FuncMap{
	"ordinal":     func(i int) int { return i + 1 },
	"subtractOne": func(i int) int { return i - 1 },
	"x":           func(i int) int { return 60 + i*70 },
	"y":           func(c testCampaignCoverageRegressionChoice) float64 { return 220 - 180*c.CurrentRate },
	"rate":        func(v float64) string { return fmt.Sprintf("%.2f%%", 100*v) },
}).Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Campaign coverage trend</title><style>body{font:16px system-ui;margin:2rem;max-width:1100px}table{border-collapse:collapse;width:100%}td,th{padding:.5rem;border:1px solid #ccc;text-align:left}svg{width:100%;max-width:800px}a{color:#1463bb}.failed{color:#b42318}code{overflow-wrap:anywhere}</style><h1>Campaign coverage trend</h1><p>{{.Scenario}} / {{.Profile}}. Points follow declared input order. All drops and coverage losses compare with the first point. Rates count exercised events divided by planned events; they do not measure application SLO success or statistical confidence. Unplanned choices have no chart point.</p>
{{$labels := .ChoiceLabels}}{{$points := .Points}}{{range $choice := (index .Points 0).Checks}}<h2>Choice {{$choice.Index}}: {{index $labels $choice.Index}}</h2><svg viewBox="0 0 760 260" role="img" aria-label="Exercised-event rate for choice {{$choice.Index}}"><path d="M60 40V220H730" fill="none" stroke="#777"/><text x="0" y="45">100%</text><text x="15" y="220">0%</text>{{range $i,$p := $points}}{{$c := index $p.Checks $choice.Index}}{{if $c.Planned}}{{if $i}}{{$prev := index (index $points (subtractOne $i)).Checks $choice.Index}}<line x1="{{x (subtractOne $i)}}" y1="{{y $prev}}" x2="{{x $i}}" y2="{{y $c}}" stroke="#1463bb"/>{{end}}<a href="#{{$p.Anchor}}"><circle cx="{{x $i}}" cy="{{y $c}}" r="6" fill="{{if eq $c.Status "failed"}}#b42318{{else}}#1463bb{{end}}"><title>{{$p.Label}}: {{rate $c.CurrentRate}}; {{$c.Status}}</title></circle></a>{{end}}<text x="{{x $i}}" y="245">{{ordinal $i}}</text>{{end}}</svg>{{end}}
{{range .Points}}<section id="{{.Anchor}}"><h2>{{.Label}}</h2><p>Run ID: <code>{{.RunID}}</code><br>Report SHA256: <code>{{.ReportSHA256}}</code></p><table><tr><th>Choice</th><th>Exercised / planned</th><th>Rate</th><th>Baseline rate</th><th>Drop / allowance (points)</th><th>Status</th><th>Finding</th></tr>{{range .Checks}}<tr><td>{{.Index}}</td><td>{{.CurrentExercised}} / {{.Planned}}</td><td>{{if .Planned}}{{rate .CurrentRate}}{{else}}unplanned{{end}}</td><td>{{if .Planned}}{{rate .BaselineRate}}{{else}}unplanned{{end}}</td><td>{{if .MaxDropPercentagePoints}}{{.DropPercentagePoints}} / {{.MaxDropPercentagePoints}}{{else}}—{{end}}</td><td class="{{.Status}}">{{.Status}}</td><td>{{.Error}}</td></tr>{{end}}</table></section>{{end}}</html>`))
