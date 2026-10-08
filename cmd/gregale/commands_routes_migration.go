package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

const routeMigrationReviewVersion = 1

type routeMigrationReviewReport struct {
	Version         int                                `json:"version"`
	GeneratedAt     time.Time                          `json:"generated_at"`
	Outcome         string                             `json:"outcome"`
	Summary         routeMigrationReviewSummary        `json:"summary"`
	FromDeployments []routeMigrationDeploymentEvidence `json:"from_deployments"`
	ToDeployments   []routeMigrationDeploymentEvidence `json:"to_deployments"`
	Mappings        []routeMigrationRouteReview        `json:"mappings"`
	Caveats         []string                           `json:"caveats"`
}

type routeMigrationReviewSummary struct {
	Mappings              int `json:"mappings"`
	SuccessorPairs        int `json:"successor_pairs"`
	NoSupportedBreaks     int `json:"no_supported_breaks"`
	BreakingPairs         int `json:"breaking_pairs"`
	ReviewRequiredPairs   int `json:"review_required_pairs"`
	UnknownPairs          int `json:"unknown_pairs"`
	MappingsWithoutTarget int `json:"mappings_without_successor"`
}

type routeMigrationDeploymentEvidence struct {
	App            string `json:"app"`
	DeploymentID   string `json:"deployment_id"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	ContractSource string `json:"contract_source,omitempty"`
	ContractSHA    string `json:"contract_sha256,omitempty"`
	CapturedAt     string `json:"captured_at,omitempty"`
}

type routeMigrationRouteReview struct {
	From       previewCustomerMigrationEndpoint `json:"from"`
	Status     string                           `json:"status"`
	Successors []routeMigrationPairReview       `json:"successors"`
	Findings   []openapidiff.RoutePairFinding   `json:"findings,omitempty"`
}

type routeMigrationPairReview struct {
	To       previewCustomerMigrationEndpoint `json:"to"`
	Status   string                           `json:"status"`
	Findings []openapidiff.RoutePairFinding   `json:"findings"`
}

func cmdRoutesMigration(args []string) int {
	if len(args) > 0 && (args[0] == "policy" || args[0] == "authorize" || args[0] == "server-check") {
		return cmdRouteRemovalServer(args[0], args[1:])
	}
	if len(args) > 0 && (args[0] == "gate" || args[0] == "approve") {
		return cmdRoutesMigrationGate(args[1:], args[0] == "approve")
	}
	if len(args) > 0 && args[0] == "readiness" {
		return cmdRouteMigrationCutoverReview(args[1:], true)
	}
	if len(args) > 0 && args[0] == "suggest" {
		return cmdRoutesMigrationSuggest(args[1:])
	}
	if len(args) == 0 || args[0] != "review" {
		PrintUsage(osStderr, "usage: gregale routes migration <suggest|review|readiness|gate|approve> [options]", "cli")
		return 1
	}
	return cmdRoutesMigrationReview(args[1:])
}

func cmdRoutesMigrationReview(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-breaking", "fail-on-incomplete")
	fs := newFlagSet("routes migration review", flag.ContinueOnError)
	mappingPath := fs.String("mapping", "", "version 1 explicit old-to-successor route mapping JSON")
	format := fs.String("format", "text", "report format: text or markdown (or use --json)")
	output := fs.String("out", "", "write the machine-readable report to a new JSON file")
	failBreaking := fs.Bool("fail-on-breaking", false, "exit nonzero when a mapped successor has a declared breaking change")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit nonzero when a mapped route lacks complete contract evidence")
	var fromValues, toValues previewCustomerMigrationDeployments
	fs.Var(&fromValues, "from-deployment", "immutable baseline deployment as APP=ID; repeat for each app")
	fs.Var(&toValues, "to-deployment", "immutable successor deployment as APP=ID; repeat for each app")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || *mappingPath == "" || !slices.Contains([]string{"text", "markdown"}, *format) ||
		(jsonOutput && *format != "text") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes migration review --mapping <PATH> --from-deployment APP=ID [--from-deployment APP=ID...] --to-deployment APP=ID [--to-deployment APP=ID...] [--format text|markdown] [--out PATH] [--fail-on-breaking] [--fail-on-incomplete] [--json]", "cli")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return routeImpactError("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	mappings, err := readPreviewCustomerMigrationMappings(*mappingPath)
	if err != nil {
		return printErr("Could not read route successor mapping", err)
	}
	if len(mappings) == 0 {
		return printErr("Invalid route successor mapping", errors.New("mapping must contain at least one source route"))
	}
	if len(mappings) > previewCustomerMigrationMaxLinks {
		return printErr("Invalid route successor mapping", fmt.Errorf("source route count exceeds the %d-link limit", previewCustomerMigrationMaxLinks))
	}
	fromDeployments, err := parsePreviewCustomerMigrationDeployments(fromValues)
	if err != nil {
		return printErr("Invalid --from-deployment", err)
	}
	toDeployments, err := parsePreviewCustomerMigrationDeployments(toValues)
	if err != nil {
		return printErr("Invalid --to-deployment", err)
	}
	fromApps, toApps, pairCount := routeMigrationRequiredApps(mappings)
	if pairCount > previewCustomerMigrationMaxLinks {
		return printErr("Invalid route successor mapping", fmt.Errorf("successor pair count exceeds the %d-link limit", previewCustomerMigrationMaxLinks))
	}
	if len(fromApps) > previewCustomerMigrationMaxApps || len(toApps) > previewCustomerMigrationMaxApps {
		return printErr("Invalid route successor mapping", fmt.Errorf("each side of the mapping may reference at most %d apps", previewCustomerMigrationMaxApps))
	}
	if err := validateRouteMigrationDeploymentSet("from", fromApps, fromDeployments); err != nil {
		return printErr("Invalid --from-deployment", err)
	}
	if err := validateRouteMigrationDeploymentSet("to", toApps, toDeployments); err != nil {
		return printErr("Invalid --to-deployment", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fromEvidence, err := readRouteMigrationDeploymentSet(ctx, client, fromDeployments)
	if err != nil {
		return printErr("Could not read baseline deployments", err)
	}
	toEvidence, err := readRouteMigrationDeploymentSet(ctx, client, toDeployments)
	if err != nil {
		return printErr("Could not read successor deployments", err)
	}
	report, err := buildRouteMigrationReviewReport(mappings, fromEvidence, toEvidence, time.Now().UTC())
	if err != nil {
		return printErr("Could not compare route contracts", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode route migration review", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save route migration review", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		var rendered bytes.Buffer
		if *format == "markdown" {
			renderRouteMigrationReviewMarkdown(&rendered, report)
		} else {
			renderRouteMigrationReviewText(&rendered, report)
		}
		if _, err := osStdout.Write(rendered.Bytes()); err != nil {
			return printErr("Could not write route migration review", err)
		}
	}
	if *failBreaking && report.Summary.BreakingPairs > 0 {
		return 1
	}
	if *failIncomplete && report.Summary.UnknownPairs+report.Summary.MappingsWithoutTarget > 0 {
		return 1
	}
	return 0
}

type routeMigrationLoadedDeployment struct {
	captureSHA string
	evidence   routeMigrationDeploymentEvidence
	spec       *openapidiff.Spec
	deployment api.DeploymentResponse
}

func routeMigrationRequiredApps(mappings map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping) (map[string]bool, map[string]bool, int) {
	fromApps, toApps := map[string]bool{}, map[string]bool{}
	pairs := 0
	for _, mapping := range mappings {
		fromApps[mapping.From.App] = true
		for _, successor := range mapping.Successors {
			toApps[successor.App] = true
			pairs++
		}
	}
	return fromApps, toApps, pairs
}

func validateRouteMigrationDeploymentSet(side string, required map[string]bool, supplied map[string]string) error {
	for app := range required {
		if supplied[app] == "" {
			return fmt.Errorf("supply --%s-deployment %s=UUID for every mapped app", side, app)
		}
	}
	for app := range supplied {
		if !required[app] {
			return fmt.Errorf("app %s has no route on the %s side of the mapping", app, side)
		}
	}
	return nil
}

func readRouteMigrationDeploymentSet(ctx context.Context, client *api.Client, deployments map[string]string) (map[string]routeMigrationLoadedDeployment, error) {
	return readRouteMigrationDeploymentSetAllowEmpty(ctx, client, deployments, false)
}

func readRouteMigrationDeploymentSetAllowEmpty(ctx context.Context, client *api.Client, deployments map[string]string, allowEmpty bool) (map[string]routeMigrationLoadedDeployment, error) {
	apps := make([]string, 0, len(deployments))
	for app := range deployments {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	result := make(map[string]routeMigrationLoadedDeployment, len(apps))
	for _, app := range apps {
		id := deployments[app]
		deployment, err := client.GetDeployment(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read deployment %s for app %s: %w", id, app, err)
		}
		if deployment.ID != id || deployment.AppID == "" {
			return nil, fmt.Errorf("deployment %s returned incomplete identity for app %s", id, app)
		}
		inventory, spec := readRouteLifecycleInventory(ctx, client, app, id, deployment.AppID)
		if inventory.Status != "available" && !(allowEmpty && inventory.Reason == "deployment_contract_has_no_paths") {
			spec = nil
		}
		result[app] = routeMigrationLoadedDeployment{
			deployment: deployment,
			captureSHA: inventory.CaptureSHA256,
			evidence: routeMigrationDeploymentEvidence{
				App: app, DeploymentID: id, Status: inventory.Status, Reason: inventory.Reason,
				ContractSource: inventory.CaptureSource, ContractSHA: inventory.DocumentSHA256, CapturedAt: inventory.CapturedAt,
			},
			spec: spec,
		}
	}
	return result, nil
}

func buildRouteMigrationReviewReport(
	mappings map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping,
	fromDeployments, toDeployments map[string]routeMigrationLoadedDeployment,
	generated time.Time,
) (routeMigrationReviewReport, error) {
	report := routeMigrationReviewReport{
		Version: routeMigrationReviewVersion, GeneratedAt: generated.UTC(),
		Outcome: "no_supported_breaks", Mappings: []routeMigrationRouteReview{},
		FromDeployments: routeMigrationDeploymentRows(fromDeployments), ToDeployments: routeMigrationDeploymentRows(toDeployments),
		Caveats: []string{
			"The review compares captured OpenAPI declarations, not runtime behavior, client code, data transformations, or undocumented semantics.",
			"Deployment IDs identify the compared revisions, but an owner can replace a deployment's contract capture; source, capture time, and SHA-256 identify the contract bytes reviewed.",
			"no_supported_breaks means supported request, response, method, path-parameter, and security checks found no declared break; it is not proof that the successor is behaviorally equivalent.",
			"Path parameters are paired by position; a changed parameter position remains unknown. Missing, truncated, unsupported, or unbound deployment contracts remain unknown. Review each route mapping with its owner before changing or removing a route.",
		},
	}
	keys := make([]previewCustomerMigrationRouteKey, 0, len(mappings))
	for key := range mappings {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := mappings[keys[i]].From, mappings[keys[j]].From
		return a.App+"\x00"+a.Method+"\x00"+a.Path < b.App+"\x00"+b.Method+"\x00"+b.Path
	})
	for _, key := range keys {
		mapping := mappings[key]
		review := routeMigrationRouteReview{From: mapping.From, Successors: []routeMigrationPairReview{}}
		if len(mapping.Successors) == 0 {
			review.Status = "unknown"
			review.Findings = []openapidiff.RoutePairFinding{{Severity: "unknown", Code: "no_successor_declared"}}
			report.Summary.MappingsWithoutTarget++
			report.Summary.UnknownPairs++
			report.Mappings = append(report.Mappings, review)
			continue
		}
		successors := append([]previewCustomerMigrationEndpoint(nil), mapping.Successors...)
		sort.Slice(successors, func(i, j int) bool {
			a, b := successors[i], successors[j]
			return a.App+"\x00"+a.Method+"\x00"+a.Path < b.App+"\x00"+b.Method+"\x00"+b.Path
		})
		for _, successor := range successors {
			pair := routeMigrationPairReview{To: successor}
			from := fromDeployments[mapping.From.App]
			to := toDeployments[successor.App]
			if from.spec == nil {
				pair.Findings = append(pair.Findings, openapidiff.RoutePairFinding{Severity: "unknown", Code: "source_contract_unavailable"})
			}
			if to.spec == nil {
				pair.Findings = append(pair.Findings, openapidiff.RoutePairFinding{Severity: "unknown", Code: "successor_contract_unavailable"})
			}
			if from.spec != nil && to.spec != nil {
				comparison, err := openapidiff.CompareRoutePair(from.spec, mapping.From.Method, mapping.From.Path, to.spec, successor.Method, successor.Path)
				if err != nil {
					return routeMigrationReviewReport{}, fmt.Errorf("compare %s %s to %s %s: %w", mapping.From.Method, mapping.From.Path, successor.Method, successor.Path, err)
				}
				pair.Status, pair.Findings = comparison.Status, comparison.Findings
			} else {
				pair.Status = "unknown"
				sortRouteMigrationFindings(pair.Findings)
			}
			report.Summary.SuccessorPairs++
			switch pair.Status {
			case "no_supported_breaks":
				report.Summary.NoSupportedBreaks++
			case "breaking":
				report.Summary.BreakingPairs++
			case "review_required":
				report.Summary.ReviewRequiredPairs++
			default:
				report.Summary.UnknownPairs++
			}
			review.Successors = append(review.Successors, pair)
		}
		review.Status = routeMigrationMappingStatus(review.Successors)
		report.Mappings = append(report.Mappings, review)
	}
	report.Summary.Mappings = len(report.Mappings)
	switch {
	case report.Summary.BreakingPairs > 0:
		report.Outcome = "breaking_changes"
	case report.Summary.UnknownPairs > 0:
		report.Outcome = "incomplete"
	case report.Summary.ReviewRequiredPairs > 0:
		report.Outcome = "review_required"
	}
	return report, nil
}

func routeMigrationMappingStatus(successors []routeMigrationPairReview) string {
	if len(successors) == 0 {
		return "unknown"
	}
	statuses := map[string]bool{}
	for _, successor := range successors {
		statuses[successor.Status] = true
	}
	if len(statuses) == 1 {
		for status := range statuses {
			return status
		}
	}
	if statuses["no_supported_breaks"] {
		return "successor_options"
	}
	if statuses["breaking"] {
		return "breaking"
	}
	if statuses["unknown"] {
		return "unknown"
	}
	return "review_required"
}

func sortRouteMigrationFindings(findings []openapidiff.RoutePairFinding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		return a.Severity+"\x00"+a.Code+"\x00"+a.Location < b.Severity+"\x00"+b.Code+"\x00"+b.Location
	})
}

func routeMigrationDeploymentRows(deployments map[string]routeMigrationLoadedDeployment) []routeMigrationDeploymentEvidence {
	apps := make([]string, 0, len(deployments))
	for app := range deployments {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	rows := make([]routeMigrationDeploymentEvidence, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, deployments[app].evidence)
	}
	return rows
}

func renderRouteMigrationReviewText(w io.Writer, report routeMigrationReviewReport) {
	_, _ = fmt.Fprintf(w, "Route migration compatibility review\nOutcome: %s; mappings: %d; successor pairs: %d; no supported breaks: %d; breaking: %d; review: %d; unknown: %d\n",
		report.Outcome, report.Summary.Mappings, report.Summary.SuccessorPairs, report.Summary.NoSupportedBreaks,
		report.Summary.BreakingPairs, report.Summary.ReviewRequiredPairs, report.Summary.UnknownPairs)
	for _, side := range []struct {
		name string
		rows []routeMigrationDeploymentEvidence
	}{{name: "Baseline", rows: report.FromDeployments}, {name: "Successor", rows: report.ToDeployments}} {
		if len(side.rows) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "%s contracts:\n", side.name)
		for _, deployment := range side.rows {
			_, _ = fmt.Fprintf(w, "  %s deployment %s: contract %s", deployment.App, deployment.DeploymentID, deployment.Status)
			if deployment.ContractSource != "" {
				_, _ = fmt.Fprintf(w, ", source %s", deployment.ContractSource)
			}
			if deployment.ContractSHA != "" {
				_, _ = fmt.Fprintf(w, ", sha256 %s", deployment.ContractSHA)
			}
			if deployment.CapturedAt != "" {
				_, _ = fmt.Fprintf(w, ", captured %s", deployment.CapturedAt)
			}
			if deployment.Reason != "" {
				_, _ = fmt.Fprintf(w, " (%s)", deployment.Reason)
			}
			_, _ = fmt.Fprintln(w)
		}
	}
	for _, mapping := range report.Mappings {
		_, _ = fmt.Fprintf(w, "\n%s %s %s: %s\n", mapping.From.App, mapping.From.Method, mapping.From.Path, mapping.Status)
		for _, successor := range mapping.Successors {
			_, _ = fmt.Fprintf(w, "  -> %s %s %s: %s", successor.To.App, successor.To.Method, successor.To.Path, successor.Status)
			if len(successor.Findings) == 0 {
				_, _ = fmt.Fprintln(w)
				continue
			}
			_, _ = fmt.Fprintln(w)
			for _, finding := range successor.Findings {
				_, _ = fmt.Fprintf(w, "     %s %s", finding.Severity, finding.Code)
				if finding.Location != "" {
					_, _ = fmt.Fprintf(w, " at %s", finding.Location)
				}
				_, _ = fmt.Fprintln(w)
			}
		}
		for _, finding := range mapping.Findings {
			_, _ = fmt.Fprintf(w, "  %s %s\n", finding.Severity, finding.Code)
		}
	}
	if len(report.Caveats) > 0 {
		_, _ = fmt.Fprint(w, "\nCaveats:\n")
		for _, caveat := range report.Caveats {
			_, _ = fmt.Fprintf(w, "  - %s\n", caveat)
		}
	}
	_, _ = fmt.Fprintln(w, "This compares declared contracts; it does not prove runtime behavior or automatically authorize route removal.")
}

func renderRouteMigrationReviewMarkdown(w io.Writer, report routeMigrationReviewReport) {
	_, _ = fmt.Fprintf(w, "## Route migration compatibility review\n\nOutcome: **%s**. Mappings: **%d**; successor pairs: **%d**; no supported breaks: **%d**; breaking: **%d**; review: **%d**; unknown: **%d**.\n\n",
		report.Outcome, report.Summary.Mappings, report.Summary.SuccessorPairs, report.Summary.NoSupportedBreaks,
		report.Summary.BreakingPairs, report.Summary.ReviewRequiredPairs, report.Summary.UnknownPairs)
	for _, side := range []struct {
		name string
		rows []routeMigrationDeploymentEvidence
	}{{name: "Baseline contracts", rows: report.FromDeployments}, {name: "Successor contracts", rows: report.ToDeployments}} {
		if len(side.rows) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "**%s**\n\n", side.name)
		for _, deployment := range side.rows {
			_, _ = fmt.Fprintf(w, "- `%s` deployment `%s`: **%s**", previewReportText(deployment.App), deployment.DeploymentID, deployment.Status)
			if deployment.ContractSource != "" {
				_, _ = fmt.Fprintf(w, ", source `%s`", deployment.ContractSource)
			}
			if deployment.CapturedAt != "" {
				_, _ = fmt.Fprintf(w, ", captured `%s`", previewReportText(deployment.CapturedAt))
			}
			if deployment.ContractSHA != "" {
				_, _ = fmt.Fprintf(w, ", SHA-256 `%s`", deployment.ContractSHA)
			}
			if deployment.Reason != "" {
				_, _ = fmt.Fprintf(w, " (%s)", deployment.Reason)
			}
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintln(w)
	}
	for _, mapping := range report.Mappings {
		_, _ = fmt.Fprintf(w, "### %s `%s %s` — %s\n\n", previewReportText(mapping.From.App), previewReportText(mapping.From.Method), previewReportText(mapping.From.Path), mapping.Status)
		for _, successor := range mapping.Successors {
			_, _ = fmt.Fprintf(w, "- `%s %s %s`: **%s**", previewReportText(successor.To.App), previewReportText(successor.To.Method), previewReportText(successor.To.Path), successor.Status)
			if len(successor.Findings) == 0 {
				_, _ = fmt.Fprintln(w)
				continue
			}
			_, _ = fmt.Fprintln(w)
			for _, finding := range successor.Findings {
				_, _ = fmt.Fprintf(w, "  - %s `%s`", finding.Severity, finding.Code)
				if finding.Location != "" {
					_, _ = fmt.Fprintf(w, " at `%s`", previewReportText(finding.Location))
				}
				_, _ = fmt.Fprintln(w)
			}
		}
		for _, finding := range mapping.Findings {
			_, _ = fmt.Fprintf(w, "- %s `%s`\n", finding.Severity, finding.Code)
		}
		_, _ = fmt.Fprintln(w)
	}
	if len(report.Caveats) > 0 {
		_, _ = fmt.Fprint(w, "**Caveats**\n\n")
		for _, caveat := range report.Caveats {
			_, _ = fmt.Fprintf(w, "- %s\n", caveat)
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintln(w, "Declared OpenAPI evidence only; this review does not prove runtime behavior or authorize route removal.")
}
