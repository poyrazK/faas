package mcphosting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Replay bounds (ADR-20261122194324), from the limits table.
const (
	ReplayMaxResources = api.MCPReplayMaxResources
	ReplayMaxRuns      = api.MCPReplayMaxRuns
)

// ReplayOptions selects what replay runs beyond the catalog comparison.
// Tool calls run only for tools named in AllowTools, and only with fixture
// arguments: a tool's readOnlyHint never permits a call.
type ReplayOptions struct {
	Runs       int
	Fixtures   *SmokeSuite
	AllowTools []string
}

// ReplayProbe is one request sent to both deployments.
type ReplayProbe struct {
	Kind   string `json:"kind"` // resource, prompt or tool
	Target string `json:"target"`
	// Same reports equal results on every run; results are compared as JSON.
	Same      bool       `json:"same"`
	Serving   ReplaySide `json:"serving"`
	Candidate ReplaySide `json:"candidate"`
	Fixture   string     `json:"fixture,omitempty"`
	Outcome   string     `json:"outcome"` // same, changed, new_error, fixed_error
}

// ReplaySide is one deployment's view of a probe.
type ReplaySide struct {
	Errors     int   `json:"errors"`
	MedianMS   int64 `json:"median_ms"`
	MaximumMS  int64 `json:"maximum_ms"`
	durations  []int64
	lastResult []byte
	lastError  string
}

// ReplayReport is the comparison of a candidate with the serving deployment.
// OK is false on a breaking catalog change or a probe that newly fails.
type ReplayReport struct {
	OK       bool          `json:"ok"`
	Catalog  ContractDiff  `json:"catalog"`
	Probes   []ReplayProbe `json:"probes"`
	Skipped  []string      `json:"skipped,omitempty"`
	Problems []string      `json:"problems,omitempty"`
}

// Replay compares candidate with serving using read-only discovery, static
// resource reads, fixture prompts and owner-allowed fixture tool calls. It
// captures and stores no production traffic.
func Replay(ctx context.Context, serving, candidate *Client, options ReplayOptions) ReplayReport {
	report := ReplayReport{OK: true}
	runs := options.Runs
	if runs < 1 {
		runs = 3
	}
	runs = min(runs, ReplayMaxRuns)
	before, _, err := serving.DiscoverCatalog(ctx)
	if err != nil {
		return ReplayReport{Problems: []string{"serving catalog: " + err.Error()}}
	}
	after, _, err := candidate.DiscoverCatalog(ctx)
	if err != nil {
		return ReplayReport{Problems: []string{"candidate catalog: " + err.Error()}}
	}
	beforeContract, err1 := NewCatalogContract(serving.Version, before)
	afterContract, err2 := NewCatalogContract(candidate.Version, after)
	if err1 != nil || err2 != nil {
		return ReplayReport{Problems: []string{fmt.Sprintf("catalog contract: %v %v", err1, err2)}}
	}
	if report.Catalog, err = CompareContracts(beforeContract, afterContract); err != nil {
		return ReplayReport{Problems: []string{"catalog comparison: " + err.Error()}}
	}
	report.OK = !report.Catalog.Breaking
	for _, probe := range replayProbes(before, after, options, &report) {
		for range runs {
			probe.Serving.observe(probe.run(ctx, serving, before))
			probe.Candidate.observe(probe.run(ctx, candidate, after))
			probe.compareRun()
		}
		probe.finish()
		if probe.Outcome == "new_error" {
			report.OK = false
		}
		report.Probes = append(report.Probes, probe.ReplayProbe)
	}
	return report
}

type replayProbe struct {
	ReplayProbe
	args       map[string]any
	promptArgs map[string]string
	differs    bool
}

// replayProbes lists the probes both catalogs can answer: static resources
// present on both sides, then fixture prompts and allowed fixture tools.
func replayProbes(before, after Catalog, options ReplayOptions, report *ReplayReport) []*replayProbe {
	var probes []*replayProbe
	candidateResources := map[string]bool{}
	for _, r := range after.Resources {
		candidateResources[r.URI] = true
	}
	uris := make([]string, 0, len(before.Resources))
	for _, r := range before.Resources {
		if candidateResources[r.URI] {
			uris = append(uris, r.URI)
		}
	}
	sort.Strings(uris)
	if len(uris) > ReplayMaxResources {
		report.Skipped = append(report.Skipped, fmt.Sprintf("%d static resources beyond the first %d", len(uris)-ReplayMaxResources, ReplayMaxResources))
		uris = uris[:ReplayMaxResources]
	}
	for _, uri := range uris {
		probes = append(probes, &replayProbe{ReplayProbe: ReplayProbe{Kind: "resource", Target: uri}})
	}
	if options.Fixtures == nil {
		return probes
	}
	for _, fixture := range options.Fixtures.Cases {
		switch fixture.Kind {
		case "prompt":
			probes = append(probes, &replayProbe{ReplayProbe: ReplayProbe{Kind: "prompt", Target: fixture.Target, Fixture: fixture.Name}, promptArgs: fixture.PromptArguments})
		case "tool":
			if !slices.Contains(options.AllowTools, fixture.Target) {
				report.Skipped = append(report.Skipped, fmt.Sprintf("tool fixture %q: %s is not in --allow-tool", fixture.Name, fixture.Target))
				continue
			}
			probes = append(probes, &replayProbe{ReplayProbe: ReplayProbe{Kind: "tool", Target: fixture.Target, Fixture: fixture.Name}, args: fixture.Arguments})
		default:
			// Resource and auth fixtures are not compared yet; say so rather
			// than report a pass that never probed them.
			report.Skipped = append(report.Skipped, fmt.Sprintf("%s fixture %q: replay compares only prompt and tool fixtures", fixture.Kind, fixture.Name))
		}
	}
	return probes
}

type replayRun struct {
	result   []byte
	duration int64
	err      string
}

func (p *replayProbe) run(ctx context.Context, client *Client, catalog Catalog) replayRun {
	var exchange Exchange
	var err error
	switch p.Kind {
	case "resource":
		exchange, err = client.ReadResource(ctx, p.Target)
	case "prompt":
		exchange, err = client.GetPrompt(ctx, p.Target, p.promptArgs)
	case "tool":
		i := slices.IndexFunc(catalog.Tools, func(t Tool) bool { return t.Name == p.Target })
		if i < 0 {
			return replayRun{err: "tool not in catalog"}
		}
		exchange, err = client.Call(ctx, catalog.Tools[i], p.args, false)
	}
	if err != nil {
		return replayRun{duration: exchange.DurationMS, err: err.Error()}
	}
	return replayRun{result: canonicalJSON(exchange.Result), duration: exchange.DurationMS}
}

func (s *ReplaySide) observe(run replayRun) {
	s.durations = append(s.durations, run.duration)
	s.lastResult, s.lastError = run.result, run.err
	if run.err != "" {
		s.Errors++
	}
}

func (p *replayProbe) compareRun() {
	if p.Serving.lastError != p.Candidate.lastError || !bytes.Equal(p.Serving.lastResult, p.Candidate.lastResult) {
		p.differs = true
	}
}

func (p *replayProbe) finish() {
	for _, side := range []*ReplaySide{&p.Serving, &p.Candidate} {
		sorted := slices.Clone(side.durations)
		slices.Sort(sorted)
		if len(sorted) > 0 {
			side.MedianMS, side.MaximumMS = sorted[len(sorted)/2], sorted[len(sorted)-1]
		}
	}
	p.Same = !p.differs
	switch {
	case p.Candidate.Errors > p.Serving.Errors:
		p.Outcome = "new_error"
	case p.Candidate.Errors < p.Serving.Errors:
		p.Outcome = "fixed_error"
	case p.differs:
		p.Outcome = "changed"
	default:
		p.Outcome = "same"
	}
}

// canonicalJSON re-encodes a result with sorted object keys, so key order
// never reads as a change.
func canonicalJSON(raw json.RawMessage) []byte {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return raw
	}
	out, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return out
}
