package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdDebugProfiles(args []string) int {
	fs := newFlagSet("debug profiles", flag.ContinueOnError)
	deployment := fs.String("deployment-id", "", "candidate deployment UUID")
	runtime := fs.String("runtime", "", "runtime name, e.g. node22, python312, go124")
	start := fs.String("start", "", "capture start, RFC3339")
	end := fs.String("end", "", "capture end, RFC3339")
	route := fs.String("route", "", "static route label, e.g. GET /orders/{id}, or [unattributed]")
	baseline := fs.String("baseline-id", "", "baseline deployment UUID for comparison")
	baselineStart := fs.String("baseline-start", "", "baseline start, RFC3339")
	baselineEnd := fs.String("baseline-end", "", "baseline end, RFC3339")
	kind := fs.String("type", "cpu", "profile kind: cpu or heap (heap shows the live heap near --end)")
	flags, positional := normalizeDebugFlagArgs(args, map[string]bool{"route": true, "deployment-id": true, "runtime": true, "start": true, "end": true, "baseline-id": true, "baseline-start": true, "baseline-end": true, "type": true})
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(osStderr, "usage: gregale debug profiles <slug> --deployment-id UUID --runtime NAME --start RFC3339 --end RFC3339 [--type cpu|heap] [--route LABEL] [--baseline-id UUID --baseline-start RFC3339 --baseline-end RFC3339]", debugCmdDocsTopic)
		return 1
	}
	q, err := profileCLIQuery(*deployment, *runtime, *start, *end)
	if err != nil {
		return printErr("Invalid profile window", err)
	}
	q.Route = *route
	if !api.ValidProfileRoute(q.Route) {
		return printErr("Invalid route label", fmt.Errorf("use a static method/pattern or [unattributed]"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *kind == "heap" {
		return printHeapProfile(client, positional[0], q, *baseline != "" || q.Route != "")
	}
	if *kind != "cpu" {
		return printErr("Invalid profile kind", fmt.Errorf("--type must be cpu or heap"))
	}
	if *baseline != "" {
		b, err := profileCLIQuery(*baseline, *runtime, *baselineStart, *baselineEnd)
		if err != nil {
			return printErr("Invalid baseline window", err)
		}
		b.Route = *route
		out, err := client.CompareAppProfiles(context.Background(), positional[0], api.ProfileCompareRequest{Baseline: b, Candidate: q})
		if err != nil {
			return printErr("Could not compare CPU profiles", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		if err := printProfileCoverage("Baseline", out.Baseline.Coverage); err != nil {
			return printErr("Could not write profile coverage", err)
		}
		if err := printProfileCoverage("Candidate", out.Candidate.Coverage); err != nil {
			return printErr("Could not write profile coverage", err)
		}
		if err := printProfileRoutes("Baseline", out.Baseline.Routes); err != nil {
			return printErr("Could not write route costs", err)
		}
		if err := printProfileRoutes("Candidate", out.Candidate.Routes); err != nil {
			return printErr("Could not write route costs", err)
		}
		if err := printProfileAttribution("Baseline", out.Baseline.Attribution); err != nil {
			return printErr("Could not write attribution quality", err)
		}
		if err := printProfileAttribution("Candidate", out.Candidate.Attribution); err != nil {
			return printErr("Could not write attribution quality", err)
		}
		if out.Attribution != nil {
			for _, warning := range out.Attribution.Warnings {
				if _, err := fmt.Fprintln(osStdout, warning); err != nil {
					return printErr("Could not write attribution warning", err)
				}
			}
		}
		if out.RouteAdjustment != nil {
			if _, err := fmt.Fprintln(osStdout, out.RouteAdjustment.Reason); err != nil {
				return printErr("Could not write route adjustment", err)
			}
			if out.RouteAdjustment.Available {
				if _, err := fmt.Fprintf(osStdout, "Common-mix CPU/request: baseline %.6f s, candidate %.6f s, delta %+.6f s\n", *out.RouteAdjustment.BaselineCPUSecondsPerRequest, *out.RouteAdjustment.CandidateCPUSecondsPerRequest, *out.RouteAdjustment.DeltaCPUSecondsPerRequest); err != nil {
					return printErr("Could not write route adjustment", err)
				}
			}
		}
		if !out.Comparable {
			if _, err := fmt.Fprintln(osStdout, out.Reason); err != nil {
				return printErr("Could not write CPU profiles", err)
			}
			return 0
		}
		if _, err := fmt.Fprintln(osStdout, "FUNCTION\tBASELINE CPU/s\tCANDIDATE CPU/s\tDELTA CPU/s\tBASELINE SOURCE\tCANDIDATE SOURCE"); err != nil {
			return printErr("Could not write CPU profiles", err)
		}
		for _, f := range out.Functions {
			if _, err := fmt.Fprintf(osStdout, "%q\t%s\t%s\t%s\t%q\t%q\n", f.Name, profileComparisonRate(f.BaselineCPUPerSecond, f.BaselineObserved, false), profileComparisonRate(f.CandidateCPUPerSecond, f.CandidateObserved, false), profileComparisonRate(f.DeltaCPUPerSecond, f.DeltaKnown, true), profileSourceURL(f.BaselineSource), profileSourceURL(f.CandidateSource)); err != nil {
				return printErr("Could not write CPU profiles", err)
			}
		}
		return 0
	}
	out, err := client.GetAppProfiles(context.Background(), positional[0], q)
	if err != nil {
		return printErr("Could not get CPU profile", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if err := printProfileCoverage("Collection", out.Coverage); err != nil {
		return printErr("Could not write profile coverage", err)
	}
	if err := printProfileRoutes("Collection", out.Routes); err != nil {
		return printErr("Could not write route costs", err)
	}
	if err := printProfileAttribution("Collection", out.Attribution); err != nil {
		return printErr("Could not write attribution quality", err)
	}
	if out.Source != nil && out.Source.Available {
		if _, err := fmt.Fprintf(osStdout, "Recorded source commit: %s (uploaded/generated files may differ).\n", out.Source.CommitURL); err != nil {
			return printErr("Could not write profile source", err)
		}
	}
	if out.Empty {
		if _, err := fmt.Fprintln(osStdout, "No CPU samples in this deployment window."); err != nil {
			return printErr("Could not write CPU profiles", err)
		}
		return 0
	}
	if _, err := fmt.Fprintf(osStdout, "Sampled CPU: %.6f s\nFUNCTION\tSELF CPU (s)\tTOTAL CPU (s)\tSOURCE\tSOURCE URL\n", out.CPUSeconds); err != nil {
		return printErr("Could not write CPU profiles", err)
	}
	for _, f := range out.Functions {
		if _, err := fmt.Fprintf(osStdout, "%q\t%.6f\t%.6f\t%q:%d\t%q\n", f.Name, f.SelfCPUSeconds, f.TotalCPUSeconds, f.File, f.Line, profileSourceURL(f.Source)); err != nil {
			return printErr("Could not write CPU profiles", err)
		}
	}
	return 0
}

func profileComparisonRate(rate float64, observed, delta bool) string {
	if !observed {
		if delta {
			return "Unknown"
		}
		return "Not observed"
	}
	if delta {
		return fmt.Sprintf("%+.6f", rate)
	}
	return fmt.Sprintf("%.6f", rate)
}

func profileSourceURL(source *api.ProfileSourceLocation) string {
	if source == nil {
		return "unavailable"
	}
	return source.URL
}

func printProfileCoverage(label string, c *api.ProfileCoverage) error {
	if c == nil || !c.Available {
		_, err := fmt.Fprintf(osStdout, "%s coverage unavailable; upload loss is unknown.\n", label)
		return err
	}
	_, err := fmt.Fprintf(osStdout, "%s: %d profiles, %d collectors, %.3f/%.3f s captured, %.3f s without captures; at least %d upload failures (total loss unknown).\n", label, c.ReceivedProfiles, c.ContributingCollectors, c.CoveredSeconds, c.WindowSeconds, c.GapSeconds, c.RecordedFailedUploads)
	return err
}

func profileCLIQuery(deployment, runtime, start, end string) (api.ProfileQuery, error) {
	q := api.ProfileQuery{DeploymentID: deployment, Runtime: runtime}
	var err error
	if deployment == "" || runtime == "" {
		return q, fmt.Errorf("deployment-id and runtime are required")
	}
	q.Start, err = time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return q, err
	}
	q.End, err = time.Parse(time.RFC3339Nano, end)
	if err != nil {
		return q, err
	}
	if !q.End.After(q.Start) {
		return q, fmt.Errorf("end must be after start")
	}
	return q, nil
}

func printProfileRoutes(label string, rows []api.ProfileRouteCPU) error {
	for _, row := range rows {
		cost := "unknown"
		if row.CPUSecondsPerRequest != nil {
			cost = fmt.Sprintf("%.6f", *row.CPUSecondsPerRequest)
		}
		if _, err := fmt.Fprintf(osStdout, "%s route %q: %.6f s sampled CPU; CPU/request %s s\n", label, row.Route, row.CPUSeconds, cost); err != nil {
			return err
		}
		if q := row.LabelCoverage; q != nil {
			if q.Available && q.Percent != nil {
				if _, err := fmt.Fprintf(osStdout, "  Labeled request entries: %d / %d observed (%.2f%%); boundary captures excluded: %d.\n", *q.LabeledRequests, *q.ObservedRequests, *q.Percent, q.BoundaryProfiles); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintln(osStdout, "  Labeling share unavailable:", q.Reason); err != nil {
					return err
				}
			}
		}

	}
	return nil
}

func printProfileAttribution(label string, q *api.ProfileAttributionQuality) error {
	if q == nil || !q.Available || q.AttributedPercent == nil {
		_, err := fmt.Fprintf(osStdout, "%s attribution quality unavailable.\n", label)
		return err
	}
	if _, err := fmt.Fprintf(osStdout, "%s whole-window sampled CPU: %.6f s; labeled %.2f%%, unattributed %.2f%% (discard diagnostics complete: %t).\n", label, q.TotalCPUSeconds, *q.AttributedPercent, *q.UnattributedPercent, q.DiagnosticsComplete); err != nil {
		return err
	}
	for _, r := range q.Reasons {
		if _, err := fmt.Fprintf(osStdout, "  %s: %.6f CPU seconds\n", r.Reason, r.CPUSeconds); err != nil {
			return err
		}
	}
	return nil
}

// printHeapProfile prints the continuous live heap near the query end
// (ADR-967). Heap profiles have no route labels or deployment comparison.
func printHeapProfile(client *Client, slug string, q api.ProfileQuery, unsupported bool) int {
	if unsupported {
		return printErr("Unsupported heap query", fmt.Errorf("heap profiles do not support --route or --baseline-id"))
	}
	out, err := client.GetAppHeapProfile(context.Background(), slug, q)
	if err != nil {
		return printErr("Could not get heap profile", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if _, err := fmt.Fprintf(osStdout, "Live heap between %s and %s\n", out.Query.Start.Format(time.RFC3339), out.Query.End.Format(time.RFC3339)); err != nil {
		return printErr("Could not write heap profile", err)
	}
	if err := printProfileCaptureView(out.ProfileCaptureView, 20); err != nil {
		return printErr("Could not write heap profile", err)
	}
	return 0
}
