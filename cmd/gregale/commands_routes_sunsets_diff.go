package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type routeSunsetDiff struct {
	Version           int                     `json:"version"`
	GeneratedAt       time.Time               `json:"generated_at"`
	App               string                  `json:"app"`
	DeploymentID      string                  `json:"deployment_id"`
	BeforeGeneratedAt time.Time               `json:"before_generated_at"`
	AfterGeneratedAt  time.Time               `json:"after_generated_at"`
	Outcome           string                  `json:"outcome"`
	Changes           []routeSunsetDiffChange `json:"changes"`
	Findings          []string                `json:"findings"`
	Caveats           []string                `json:"caveats"`
}
type routeSunsetDiffChange struct {
	Method               string                         `json:"method"`
	Path                 string                         `json:"path"`
	Classification       string                         `json:"classification"`
	Signals              []string                       `json:"signals"`
	Before               *routeSunsetRow                `json:"before,omitempty"`
	After                *routeSunsetRow                `json:"after,omitempty"`
	NewlyObservedCallers []api.RouteCustomerObservation `json:"newly_observed_old_route_callers"`
	ReobservedCallers    []api.RouteCustomerObservation `json:"reobserved_old_route_callers"`
}

func cmdRoutesSunsetsDiff(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-regression", "fail-on-incomplete")
	fs := newFlagSet("routes sunsets diff", flag.ContinueOnError)
	beforePath := fs.String("before", "", "previous saved sunset JSON report")
	afterPath := fs.String("after", "", "new saved sunset JSON report")
	output := fs.String("out", "", "save diff JSON to a new file")
	maxStaleness := fs.Duration("max-staleness", 24*time.Hour, "maximum age of report generation and telemetry window end")
	regression := fs.Bool("fail-on-regression", false, "exit 1 for observed regressions")
	incomplete := fs.Bool("fail-on-incomplete", false, "exit 1 for stale, changed or incomplete evidence")
	if fs.Parse(flags) != nil {
		return 1
	}
	if len(positional) != 0 || *beforePath == "" || *afterPath == "" || *maxStaleness <= 0 || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes sunsets diff --before PATH --after PATH [--max-staleness 24h] [--out PATH] [--fail-on-regression] [--fail-on-incomplete] [--json]", "cli")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return routeImpactError("Invalid --out", errors.New("choose a new file path"))
		}
	}
	before, err := readRouteSunsetReport(*beforePath)
	if err != nil {
		return routeImpactError("Could not read previous sunset report", err)
	}
	after, err := readRouteSunsetReport(*afterPath)
	if err != nil {
		return routeImpactError("Could not read current sunset report", err)
	}
	report, err := buildRouteSunsetDiff(before, after, time.Now().UTC(), *maxStaleness)
	if err != nil {
		return routeImpactError("Could not compare sunset reports", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return routeImpactError("Could not encode sunset diff", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return routeImpactError("Could not save sunset diff", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(osStdout, "Sunset changes for %s: %s\n", previewReportText(report.App), report.Outcome)
		for _, finding := range report.Findings {
			fmt.Fprintf(osStdout, "Evidence: %s\n", finding)
		}
		for _, change := range report.Changes {
			fmt.Fprintf(osStdout, "%s %s: %s (%s)\n", previewReportText(change.Method), previewReportText(change.Path), change.Classification, strings.Join(change.Signals, ", "))
			for _, caller := range change.NewlyObservedCallers {
				fmt.Fprintf(osStdout, "  newly observed consumer %s tenant %s, last %s\n", previewReportText(caller.ConsumerID), previewReportText(caller.PlatformTenantID), previewReportText(caller.LastObservedAt))
			}
			for _, caller := range change.ReobservedCallers {
				fmt.Fprintf(osStdout, "  reobserved consumer %s tenant %s, last %s\n", previewReportText(caller.ConsumerID), previewReportText(caller.PlatformTenantID), previewReportText(caller.LastObservedAt))
			}
		}
		for _, caveat := range report.Caveats {
			fmt.Fprintf(osStdout, "Note: %s\n", caveat)
		}
	}
	if *incomplete && len(report.Findings) > 0 {
		return 1
	}
	for _, change := range report.Changes {
		if *regression && change.Classification == "regression" {
			return 1
		}
		if *incomplete && (change.Classification == "evidence_degraded" || change.Classification == "changed" || sunsetDiffIncomplete(change)) {
			return 1
		}
	}
	return 0
}

func readRouteSunsetReport(path string) (routeSunsetReport, error) {
	var report routeSunsetReport
	file, err := openCustomerFile(path)
	if err != nil {
		return report, errors.New("use a readable regular sunset report without symlinks")
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil || int64(len(body)) > api.RouteImpactReportMaxBytes {
		return report, errors.New("sunset report exceeds the report size limit or could not be read")
	}
	if err := json.Unmarshal(body, &report); err != nil {
		return report, errors.New("expected sunset report JSON")
	}
	return report, nil
}

type sunsetWindow struct{ from, until time.Time }

func validateSunsetSnapshot(report routeSunsetReport) (map[string]routeSunsetRow, sunsetWindow, error) {
	fail := func(reason string) (map[string]routeSunsetRow, sunsetWindow, error) {
		return nil, sunsetWindow{}, errors.New(reason)
	}
	if report.Version != 1 || report.GeneratedAt.IsZero() || !validCLISlug(report.App) {
		return fail("expected version 1 sunset report with app and generation time")
	}
	if _, err := uuid.Parse(report.DeploymentID); err != nil {
		return fail("invalid deployment ID")
	}
	hash := report.ImportedSHA256
	if report.MetadataSource == "deployment" {
		hash = report.CaptureSHA256
	}
	digest, err := hex.DecodeString(hash)
	if err != nil || len(digest) != 32 {
		return fail("invalid imported document hash")
	}
	horizon, err := time.ParseDuration(report.Horizon)
	if err != nil || horizon <= 0 {
		return fail("invalid sunset horizon")
	}
	observation := report.Observation
	if observation.Slug != report.App || observation.DeploymentID != report.DeploymentID {
		return fail("observation app/deployment mismatch")
	}
	from, err := time.Parse(time.RFC3339Nano, observation.From)
	if err != nil {
		return fail("invalid observation start")
	}
	until, err := time.Parse(time.RFC3339Nano, observation.Until)
	if err != nil || !until.After(from) || until.After(report.GeneratedAt.Add(5*time.Second)) {
		return fail("invalid observation end")
	}
	asOf, err := time.Parse(time.RFC3339Nano, observation.AsOf)
	if err != nil || asOf.Before(until) || asOf.After(report.GeneratedAt.Add(5*time.Second)) {
		return fail("invalid observation as-of time")
	}
	rows := map[string]routeSunsetRow{}
	for _, row := range report.Routes {
		if !strings.HasPrefix(row.Path, "/") || strings.ContainsAny(row.Path, "\x00\r\n?#") || !sunsetDiffMethod(row.Method) {
			return fail("invalid operation identity")
		}
		key := row.Method + " " + row.Path
		if _, found := rows[key]; found {
			return fail("duplicate operation")
		}
		if row.Requests < 0 || row.AnonymousRequests < 0 || row.UnresolvedRequests < 0 {
			return fail("negative request counts")
		}
		if err := validateSunsetLastObserved(row.LastObservedAt, row.Requests, from, until); err != nil {
			return fail(err.Error())
		}
		for _, date := range []string{row.DeprecatedAt, row.SunsetAt} {
			if date != "" {
				if _, err := time.Parse(time.RFC3339Nano, date); err != nil {
					return fail("invalid lifecycle date")
				}
			}
		}
		seen := map[string]bool{}
		for _, caller := range row.Customers {
			key := sunsetCallerKey(caller)
			if caller.ConsumerID == "" && caller.PlatformTenantID == "" || seen[key] || caller.Requests < 0 {
				return fail("invalid or duplicate caller identity")
			}
			seen[key] = true
			if err := validateSunsetLastObserved(caller.LastObservedAt, caller.Requests, from, until); err != nil {
				return fail(err.Error())
			}
		}
		targets := map[string]bool{}
		for _, target := range row.Successors {
			key := sunsetTargetKey(target)
			if targets[key] || !validCLISlug(target.To.App) || !sunsetDiffMethod(target.To.Method) || !strings.HasPrefix(target.To.Path, "/") || target.Requests < 0 || target.CallersAlsoObserved < 0 || target.CallersAlsoObserved > len(row.Customers) {
				return fail("invalid or duplicate successor evidence")
			}
			targets[key] = true
			if target.IdentityComparable != (target.To.App == report.App) {
				return fail("invalid successor identity scope")
			}
		}
		rows[key] = row
	}
	return rows, sunsetWindow{from, until}, nil
}
func validateSunsetLastObserved(value string, count int64, from, until time.Time) error {
	if count == 0 && value == "" {
		return nil
	}
	date, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || date.Before(from) || !date.Before(until) {
		return errors.New("last observation is missing or outside its telemetry window")
	}
	return nil
}
func sunsetCallerKey(caller api.RouteCustomerObservation) string {
	return caller.ConsumerID + "\x00" + caller.PlatformTenantID
}
func sunsetTargetKey(target routeSunsetSuccessor) string {
	return target.To.App + "\x00" + target.To.Method + "\x00" + target.To.Path
}

func buildRouteSunsetDiff(before, after routeSunsetReport, now time.Time, maxAge time.Duration) (routeSunsetDiff, error) {
	result := routeSunsetDiff{Version: 1, GeneratedAt: now, App: after.App, DeploymentID: after.DeploymentID, BeforeGeneratedAt: before.GeneratedAt, AfterGeneratedAt: after.GeneratedAt, Outcome: "unchanged", Changes: []routeSunsetDiffChange{}, Findings: []string{}, Caveats: []string{
		"Observed-only reports cannot prove a caller migrated or that a route is safe to remove.",
		"Reobserved means a later old-route request was recorded; it does not prove the caller previously stopped using the route.",
		"Successor usage increases are advisory progress signals, not proof of adoption or migration order.",
		"Aggregate activity comparisons require equal-length, non-overlapping windows and unchanged contract evidence.",
	}}
	old, oldWindow, err := validateSunsetSnapshot(before)
	if err != nil {
		return result, fmt.Errorf("before: %w", err)
	}
	current, newWindow, err := validateSunsetSnapshot(after)
	if err != nil {
		return result, fmt.Errorf("after: %w", err)
	}
	if maxAge <= 0 || before.App != after.App || before.DeploymentID != after.DeploymentID || !after.GeneratedAt.After(before.GeneratedAt) || !newWindow.until.After(oldWindow.until) || newWindow.from.Before(oldWindow.from) {
		return result, errors.New("reports must use the same app and baseline deployment and advance generation time and telemetry window")
	}
	if before.GeneratedAt.After(now.Add(5*time.Second)) || after.GeneratedAt.After(now.Add(5*time.Second)) {
		return result, errors.New("report generation time is in the future")
	}
	stale := false
	// The historical baseline may be old; its telemetry must have been fresh at generation.
	if before.GeneratedAt.Sub(oldWindow.until) > maxAge {
		result.Findings = append(result.Findings, "before_telemetry_stale")
		stale = true
	}
	if now.Sub(after.GeneratedAt) > maxAge || now.Sub(newWindow.until) > maxAge {
		result.Findings = append(result.Findings, "after_evidence_stale")
		stale = true
	}
	if before.Horizon != after.Horizon {
		result.Findings = append(result.Findings, "horizon_changed")
	}
	contractChanged := !sameSunsetContracts(before.ContractReview, after.ContractReview)
	if contractChanged {
		result.Findings = append(result.Findings, "contract_review_changed")
	}
	if sunsetMetadataIdentity(before) != sunsetMetadataIdentity(after) {
		result.Findings = append(result.Findings, "metadata_document_or_source_changed")
	}
	comparableWindows := oldWindow.until.Sub(oldWindow.from) == newWindow.until.Sub(newWindow.from) && !newWindow.from.Before(oldWindow.until)
	if !comparableWindows {
		result.Findings = append(result.Findings, "activity_windows_not_comparable")
	}
	for label, report := range map[string]routeSunsetReport{"before": before, "after": after} {
		if report.Observation.Coverage != "observed_only" || report.Observation.RoutesTruncated || report.Observation.WindowClamped {
			result.Findings = append(result.Findings, label+"_telemetry_incomplete")
		}
	}
	keys := map[string]bool{}
	for key := range old {
		keys[key] = true
	}
	for key := range current {
		keys[key] = true
	}
	ordered := []string{}
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		a, hasOld := old[key]
		b, hasNew := current[key]
		change := routeSunsetDiffChange{Classification: "unchanged", Signals: []string{}, NewlyObservedCallers: []api.RouteCustomerObservation{}, ReobservedCallers: []api.RouteCustomerObservation{}}
		if hasOld {
			change.Before = &a
			change.Method, change.Path = a.Method, a.Path
		}
		if hasNew {
			change.After = &b
			change.Method, change.Path = b.Method, b.Path
		}
		switch {
		case !hasOld:
			change.Classification = "changed"
			change.Signals = append(change.Signals, "deprecated_operation_added")
		case !hasNew:
			change.Classification = "changed"
			change.Signals = append(change.Signals, "operation_missing_from_current_report")
		default:
			compareSunsetRows(&change, a, b, oldWindow, after.GeneratedAt, comparableWindows && !contractChanged && !stale && sunsetMetadataIdentity(before) == sunsetMetadataIdentity(after), sunsetTelemetryComplete(before, a) && sunsetTelemetryComplete(after, b))
		}
		result.Changes = append(result.Changes, change)
	}
	sort.Strings(result.Findings)
	for _, change := range result.Changes {
		switch change.Classification {
		case "regression":
			result.Outcome = "regressions"
		case "evidence_degraded", "changed":
			if result.Outcome != "regressions" {
				result.Outcome = "review_required"
			}
		case "evidence_updated":
			if result.Outcome == "unchanged" {
				result.Outcome = "evidence_updated"
			}
		case "improvement":
			if result.Outcome == "unchanged" || result.Outcome == "evidence_updated" {
				result.Outcome = "progress"
			}
		}
	}
	if len(result.Findings) > 0 && result.Outcome != "regressions" {
		result.Outcome = "review_required"
	}
	return result, nil
}
func sunsetTelemetryComplete(report routeSunsetReport, row routeSunsetRow) bool {
	return report.Observation.Coverage == "observed_only" && !report.Observation.RoutesTruncated && !report.Observation.WindowClamped && row.Evidence == "observed_only" && row.AnonymousRequests == 0 && row.UnresolvedRequests == 0
}
func sameSunsetContracts(a, b *routeMigrationReviewReport) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// Generation times and advisory summaries do not change capture identity.
	normal := func(review *routeMigrationReviewReport) string {
		entries := []string{}
		for side, deployments := range map[string][]routeMigrationDeploymentEvidence{"from": review.FromDeployments, "to": review.ToDeployments} {
			for _, deployment := range deployments {
				entries = append(entries, fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s", side, deployment.App, deployment.DeploymentID, deployment.Status, deployment.ContractSource, deployment.ContractSHA, deployment.CapturedAt))
			}
		}
		sort.Strings(entries)
		return strings.Join(entries, "\n")
	}
	return normal(a) == normal(b)
}
func compareSunsetRows(change *routeSunsetDiffChange, a, b routeSunsetRow, oldWindow sunsetWindow, at time.Time, aggregateComparable, complete bool) {
	add := func(signal string) { change.Signals = append(change.Signals, signal) }
	metadataChanged := a.DeprecatedAt != b.DeprecatedAt || a.SunsetAt != b.SunsetAt || a.SuccessorURL != b.SuccessorURL
	if metadataChanged {
		add("lifecycle_metadata_changed")
		change.Classification = "changed"
	}
	oldTargets, newTargets := map[string]routeSunsetSuccessor{}, map[string]routeSunsetSuccessor{}
	for _, target := range a.Successors {
		oldTargets[sunsetTargetKey(target)] = target
	}
	for _, target := range b.Successors {
		newTargets[sunsetTargetKey(target)] = target
	}
	mappingChanged := len(oldTargets) != len(newTargets)
	for key := range oldTargets {
		if _, found := newTargets[key]; !found {
			mappingChanged = true
		}
	}
	if mappingChanged {
		add("successor_mapping_changed")
		change.Classification = "changed"
	}
	if a.ContractStatus != b.ContractStatus {
		add("contract_status_changed")
		change.Classification = "changed"
	}
	degraded := !complete || len(b.Findings) > 0 || b.ContractStatus != "no_supported_breaks" || !aggregateComparable
	for _, target := range b.Successors {
		if target.Evidence != "observed_only" || target.ContractStatus != "no_supported_breaks" {
			degraded = true
		}
	}
	if degraded {
		add("evidence_incomplete_or_not_comparable")
		if change.Classification == "unchanged" {
			change.Classification = "evidence_degraded"
		}
	}
	if b.SunsetAt != "" {
		sunset, _ := time.Parse(time.RFC3339Nano, b.SunsetAt)
		last, _ := time.Parse(time.RFC3339Nano, b.LastObservedAt)
		if !sunset.After(at) && b.Requests > 0 {
			add("overdue_route_active")
			if !last.Before(sunset) {
				add("old_route_observed_after_sunset")
				change.Classification = "regression"
			}
		}
	}
	callers := map[string]api.RouteCustomerObservation{}
	for _, caller := range a.Customers {
		callers[sunsetCallerKey(caller)] = caller
	}
	for _, caller := range b.Customers {
		last, _ := time.Parse(time.RFC3339Nano, caller.LastObservedAt)
		if caller.Requests <= 0 || last.Before(oldWindow.until) {
			continue
		}
		prior, found := callers[sunsetCallerKey(caller)]
		if !found {
			change.NewlyObservedCallers = append(change.NewlyObservedCallers, caller)
			continue
		}
		priorLast, _ := time.Parse(time.RFC3339Nano, prior.LastObservedAt)
		if last.After(priorLast) {
			change.ReobservedCallers = append(change.ReobservedCallers, caller)
		}
	}
	if len(change.ReobservedCallers) > 0 {
		add("old_route_callers_reobserved")
	}
	if len(change.NewlyObservedCallers) > 0 {
		add("newly_observed_old_route_callers")
		change.Classification = "regression"
	}
	if a.Requests == 0 && b.Requests > 0 {
		last, _ := time.Parse(time.RFC3339Nano, b.LastObservedAt)
		if !last.Before(oldWindow.until) {
			add("old_route_activity_resumed")
			change.Classification = "regression"
		}
	}
	if aggregateComparable && complete && !degraded && !metadataChanged && !mappingChanged && a.ContractStatus == b.ContractStatus {
		if b.Requests < a.Requests {
			add("old_route_activity_reduced")
			if change.Classification == "unchanged" {
				change.Classification = "improvement"
			}
		}
		for key, target := range newTargets {
			previous := oldTargets[key]
			if target.Evidence == "observed_only" && previous.Evidence == "observed_only" && target.ContractStatus == "no_supported_breaks" && previous.ContractStatus == target.ContractStatus && target.Requests > previous.Requests {
				add("successor_usage_increased")
				if change.Classification == "unchanged" {
					change.Classification = "improvement"
				}
			}
		}
	}
	if len(change.Signals) == 0 && !reflect.DeepEqual(a, b) {
		add("evidence_updated")
	}
	if len(change.Signals) > 0 && change.Classification == "unchanged" {
		change.Classification = "evidence_updated"
	}
	sort.Slice(change.NewlyObservedCallers, func(i, j int) bool {
		return sunsetCallerKey(change.NewlyObservedCallers[i]) < sunsetCallerKey(change.NewlyObservedCallers[j])
	})
	sort.Slice(change.ReobservedCallers, func(i, j int) bool {
		return sunsetCallerKey(change.ReobservedCallers[i]) < sunsetCallerKey(change.ReobservedCallers[j])
	})
}

func sunsetDiffIncomplete(change routeSunsetDiffChange) bool {
	for _, signal := range change.Signals {
		switch signal {
		case "evidence_incomplete_or_not_comparable", "lifecycle_metadata_changed", "successor_mapping_changed", "contract_status_changed", "deprecated_operation_added", "operation_missing_from_current_report":
			return true
		}
	}
	return false
}

func sunsetDiffMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return true
	}
	return false
}

func sunsetMetadataIdentity(report routeSunsetReport) string {
	source := report.MetadataSource
	if source == "" {
		source = "manual_import"
	}
	return source + "|" + report.ImportedSHA256 + "|" + report.CaptureSHA256 + "|" + report.CaptureSource + "|" + report.CapturedAt
}
