package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
)

type routeSunsetReport struct {
	Version        int                            `json:"version"`
	GeneratedAt    time.Time                      `json:"generated_at"`
	App            string                         `json:"app"`
	DeploymentID   string                         `json:"deployment_id"`
	ImportedSHA256 string                         `json:"imported_sha256"`
	MetadataSource string                         `json:"metadata_source,omitempty"`
	CaptureSHA256  string                         `json:"capture_sha256,omitempty"`
	CaptureSource  string                         `json:"capture_source,omitempty"`
	CapturedAt     string                         `json:"captured_at,omitempty"`
	Horizon        string                         `json:"horizon"`
	Observation    api.RouteCustomerUsageResponse `json:"observation"`
	ContractReview *routeMigrationReviewReport    `json:"contract_review,omitempty"`
	Routes         []routeSunsetRow               `json:"routes"`
	Caveats        []string                       `json:"caveats"`
}
type routeSunsetRow struct {
	Method             string                         `json:"method"`
	Path               string                         `json:"path"`
	Status             string                         `json:"status"`
	DeprecatedAt       string                         `json:"deprecated_at,omitempty"`
	SunsetAt           string                         `json:"sunset_at,omitempty"`
	SuccessorURL       string                         `json:"successor_url,omitempty"`
	Requests           int64                          `json:"observed_requests"`
	LastObservedAt     string                         `json:"last_observed_at,omitempty"`
	Customers          []api.RouteCustomerObservation `json:"remaining_callers"`
	AnonymousRequests  int64                          `json:"anonymous_requests"`
	UnresolvedRequests int64                          `json:"unresolved_identity_requests"`
	Evidence           string                         `json:"telemetry_status"`
	ContractStatus     string                         `json:"contract_status"`
	Successors         []routeSunsetSuccessor         `json:"successors"`
	Findings           []string                       `json:"findings"`
}
type routeSunsetSuccessor struct {
	To                  previewCustomerMigrationEndpoint `json:"to"`
	ContractStatus      string                           `json:"contract_status"`
	Requests            int64                            `json:"observed_requests"`
	IdentityComparable  bool                             `json:"caller_identity_comparable"`
	CallersAlsoObserved int                              `json:"old_route_callers_also_observed"`
	Evidence            string                           `json:"telemetry_status"`
}

func cmdRoutesSunsets(args []string) int {
	if len(args) > 0 && args[0] == "diff" {
		return cmdRoutesSunsetsDiff(args[1:])
	}
	flags, positional := splitArgsForFlags(args, "fail-on-overdue", "fail-on-incomplete")
	fs := newFlagSet("routes sunsets", flag.ContinueOnError)
	source := fs.String("source", "deployment", "lifecycle metadata: deployment or manual_import")
	deployment := fs.String("deployment", "", "baseline deployment UUID (required)")
	since := fs.String("since", "14d", "retained telemetry window")
	horizon := fs.Duration("within", 30*24*time.Hour, "upcoming sunset horizon")
	mappingPath := fs.String("mapping", "", "optional explicit successor mapping JSON")
	output := fs.String("out", "", "save JSON to a new file")
	overdue := fs.Bool("fail-on-overdue", false, "exit 1 for elapsed sunset dates")
	incomplete := fs.Bool("fail-on-incomplete", false, "exit 1 for missing metadata or incomplete evidence")
	var targets previewCustomerMigrationDeployments
	fs.Var(&targets, "to-deployment", "successor APP=UUID; repeat with --mapping")
	if fs.Parse(flags) != nil {
		return 1
	}
	_, idErr := uuid.Parse(*deployment)
	if len(positional) != 1 || !validCLISlug(positional[0]) || idErr != nil || (*source != "deployment" && *source != "manual_import") || *horizon <= 0 || (*mappingPath == "") != (len(targets) == 0) || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes sunsets <slug> --deployment UUID [--source deployment|manual_import] [--since 14d] [--within 720h] [--mapping PATH --to-deployment APP=UUID...] [--out PATH] [--fail-on-overdue] [--fail-on-incomplete] [--json]", "cli")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return routeImpactError("Invalid --out", errors.New("choose a new file path"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	slug := positional[0]

	var doc []byte
	var inventory routeLifecycleInventory
	var spec *openapidiff.Spec
	if *source == "manual_import" {
		doc, err = client.GetAppOpenAPI(ctx, slug, "manual_import")
		if err != nil {
			return printErr("Could not read imported lifecycle metadata", err)
		}
		spec, err = openapidiff.LoadBytes(doc)
		if err != nil {
			return printErr("Invalid imported contract", err)
		}
	} else {
		dep, readErr := client.GetDeployment(ctx, *deployment)
		if readErr != nil || dep.ID != *deployment || dep.AppID == "" {
			return printErr("Could not read deployment identity", errors.New("deployment unavailable or incomplete"))
		}
		capture, readErr := client.GetAppsDeploymentOpenAPIDoc(ctx, slug, *deployment)
		if readErr != nil {
			return printErr("Could not read lifecycle capture", readErr)
		}
		if capture.AppID != dep.AppID || capture.DeploymentID != dep.ID || capture.Truncated || len(capture.Doc) == 0 || (capture.Source != "cold_boot" && capture.Source != "manual_upload") || len(capture.DocSHA256) != 64 {
			return printErr("Invalid lifecycle capture", errors.New("capture identity or completeness mismatch"))
		}
		digest, hashErr := hex.DecodeString(capture.DocSHA256)
		if hashErr != nil || len(digest) != 32 {
			return printErr("Invalid lifecycle capture", errors.New("invalid capture hash"))
		}
		doc, err = marshalPreviewReportDocument(capture.Doc)
		if err != nil {
			return printErr("Invalid lifecycle capture", err)
		}
		spec, err = openapidiff.LoadBytes(doc)
		if err != nil {
			return printErr("Invalid lifecycle capture", err)
		}
		inventory = routeLifecycleInventory{Source: "captured_deployment_openapi", CaptureSource: capture.Source, CaptureSHA256: capture.DocSHA256, CapturedAt: capture.CapturedAt}
	}
	usage, err := client.GetAppRouteCustomerUsage(ctx, slug, api.RouteCustomerUsageOptions{DeploymentID: *deployment, Since: *since})
	if err != nil {
		return printErr("Could not read callers", err)
	}
	if usage.Slug != slug || usage.DeploymentID != *deployment {
		return printErr("Invalid caller evidence", errors.New("app or deployment identity mismatch"))
	}
	var review *routeMigrationReviewReport
	successorUsage := map[string]api.RouteCustomerUsageResponse{}
	if *mappingPath != "" {
		mappings, err := readPreviewCustomerMigrationMappings(*mappingPath)
		if err != nil {
			return printErr("Invalid mapping", err)
		}
		fromApps, toApps, pairs := routeMigrationRequiredApps(mappings)
		if len(mappings) == 0 || len(mappings) > previewCustomerMigrationMaxLinks || pairs > previewCustomerMigrationMaxLinks || len(toApps) > previewCustomerMigrationMaxApps {
			return printErr("Invalid mapping", errors.New("mapping is empty or exceeds migration limits"))
		}
		from := map[string]string{slug: *deployment}
		to, err := parsePreviewCustomerMigrationDeployments(targets)
		if err != nil {
			return printErr("Invalid successors", err)
		}
		if err := validateRouteMigrationDeploymentSet("from", fromApps, from); err != nil {
			return printErr("Invalid mapping", err)
		}
		if err := validateRouteMigrationDeploymentSet("to", toApps, to); err != nil {
			return printErr("Invalid mapping", err)
		}
		old, err := readRouteMigrationDeploymentSet(ctx, client, from)
		if err != nil {
			return printErr("Could not read baseline", err)
		}
		next, err := readRouteMigrationDeploymentSet(ctx, client, to)
		if err != nil {
			return printErr("Could not read successors", err)
		}
		built, err := buildRouteMigrationReviewReport(mappings, old, next, time.Now().UTC())
		if err != nil {
			return printErr("Could not compare successors", err)
		}
		review = &built
		for app, id := range to {
			observed, err := client.GetAppRouteCustomerUsage(ctx, app, api.RouteCustomerUsageOptions{DeploymentID: id, Since: usage.From, Until: usage.Until})
			if err != nil {
				return printErr("Could not read successor callers", err)
			}
			if observed.Slug != app || observed.DeploymentID != id || observed.From != usage.From || observed.Until != usage.Until {
				return printErr("Invalid successor telemetry", errors.New("app, deployment or window mismatch"))
			}
			successorUsage[app] = observed
		}
	}
	report := buildRouteSunsetReport(slug, *deployment, doc, spec, usage, review, successorUsage, time.Now().UTC(), *horizon)
	report.MetadataSource = *source
	if *source == "deployment" {
		report.ImportedSHA256 = ""
		report.CaptureSHA256 = inventory.CaptureSHA256
		report.CaptureSource = inventory.CaptureSource
		report.CapturedAt = inventory.CapturedAt
		report.Caveats[0] = "Lifecycle metadata and caller activity are scoped to the selected deployment; the capture hash identifies the metadata revision."
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode sunsets", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save sunsets", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		fmt.Fprintf(osStdout, "Route sunsets for %s — deployment %s\n", previewReportText(slug), previewReportText(*deployment))
		fmt.Fprintf(osStdout, "Metadata: %s; capture source %s; capture hash %s; captured %s; import hash %s\n", report.MetadataSource, previewReportText(report.CaptureSource), previewReportText(report.CaptureSHA256), previewReportText(report.CapturedAt), previewReportText(report.ImportedSHA256))
		for _, row := range report.Routes {
			fmt.Fprintf(osStdout, "%s %s: %s; sunset %s; %d observed requests; last %s; contract %s; telemetry %s\n", previewReportText(row.Method), previewReportText(row.Path), row.Status, previewReportText(row.SunsetAt), row.Requests, previewReportText(row.LastObservedAt), row.ContractStatus, row.Evidence)
			for _, caller := range row.Customers {
				fmt.Fprintf(osStdout, "  consumer %s tenant %s: %d requests, last %s\n", previewReportText(caller.ConsumerID), previewReportText(caller.PlatformTenantID), caller.Requests, previewReportText(caller.LastObservedAt))
			}
			for _, target := range row.Successors {
				fmt.Fprintf(osStdout, "  successor %s %s %s: %s; %d requests; %d old-route callers also observed; %s\n", previewReportText(target.To.App), previewReportText(target.To.Method), previewReportText(target.To.Path), target.ContractStatus, target.Requests, target.CallersAlsoObserved, target.Evidence)
				if !target.IdentityComparable {
					fmt.Fprintln(osStdout, "    Caller identity overlap is unavailable across apps.")
				}
			}
			for _, finding := range row.Findings {
				fmt.Fprintf(osStdout, "  %s\n", finding)
			}
		}
		for _, caveat := range report.Caveats {
			fmt.Fprintf(osStdout, "Note: %s\n", caveat)
		}
	}
	for _, row := range report.Routes {
		if *overdue && row.Status == "overdue" || *incomplete && (len(row.Findings) > 0 || row.Evidence != "observed_only" || row.ContractStatus != "no_supported_breaks") {
			return 1
		}
	}
	return 0
}

func sunsetUsage(usage api.RouteCustomerUsageResponse, method, path string) (api.RouteCustomerUsage, string) {
	evidence := "observed_only"
	if usage.Coverage != "observed_only" || usage.RoutesTruncated || usage.WindowClamped || usage.From == "" || usage.Until == "" {
		evidence = "incomplete"
	}
	for _, row := range usage.Routes {
		// The API exposes either the canonical path or the gateway's method+path label.
		route := strings.TrimPrefix(row.Route, method+" ")
		if row.Method == method && route == path {
			if row.CustomersTruncated || row.OtherCustomerRequests > 0 || row.UnresolvedIdentityRequests > 0 || row.AnonymousRequests > 0 {
				evidence = "incomplete"
			}
			return row, evidence
		}
	}
	return api.RouteCustomerUsage{}, evidence
}

func buildRouteSunsetReport(app, deployment string, doc []byte, spec *openapidiff.Spec, usage api.RouteCustomerUsageResponse, review *routeMigrationReviewReport, next map[string]api.RouteCustomerUsageResponse, now time.Time, horizon time.Duration) routeSunsetReport {
	report := routeSunsetReport{Version: 1, GeneratedAt: now, App: app, DeploymentID: deployment, ImportedSHA256: fmt.Sprintf("%x", sha256.Sum256(doc)), Horizon: horizon.String(), Observation: usage, ContractReview: review, Routes: []routeSunsetRow{}, Caveats: []string{
		"Lifecycle dates come from the mutable app import; caller activity is scoped to the selected deployment.",
		"Observed-only telemetry cannot prove inactivity or migration completion; requests may be sampled, dropped or expired.",
		"Successor observations show usage in the same window, not the order of migration or compatibility beyond declared contracts.",
		"This report does not authorize removal. Use server-check and removal approval before changing production traffic.",
	}}
	for path, item := range spec.Paths {
		if item == nil {
			continue
		}
		for method, operation := range item.Methods {
			if operation == nil {
				continue
			}
			raw := operation.Raw
			_, date := raw["x-gregale-deprecated-at"]
			_, sunset := raw["x-gregale-sunset-at"]
			_, link := raw["x-gregale-successor"]
			if raw["deprecated"] != true && !date && !sunset && !link {
				continue
			}
			metadata, err := routelifecycle.Parse(raw)
			row := routeSunsetRow{Method: strings.ToUpper(method), Path: path, Status: "unscheduled", ContractStatus: "not_reviewed", Customers: []api.RouteCustomerObservation{}, Successors: []routeSunsetSuccessor{}, Findings: []string{}}
			if err != nil {
				row.Status = "invalid_metadata"
				row.Findings = append(row.Findings, "invalid_metadata: "+previewReportText(err.Error()))
			} else {
				if !metadata.DeprecatedAt.IsZero() {
					row.DeprecatedAt = metadata.DeprecatedAt.Format(time.RFC3339)
				} else {
					row.Findings = append(row.Findings, "missing_deprecation_date")
				}
				row.SuccessorURL = metadata.Successor
				if row.SuccessorURL == "" {
					row.Findings = append(row.Findings, "missing_successor_url")
				}
				if metadata.SunsetAt.IsZero() {
					row.Findings = append(row.Findings, "missing_sunset_date")
				} else {
					row.SunsetAt = metadata.SunsetAt.Format(time.RFC3339)
					switch {
					case !metadata.SunsetAt.After(now):
						row.Status = "overdue"
					case !metadata.SunsetAt.After(now.Add(horizon)):
						row.Status = "upcoming"
					default:
						row.Status = "scheduled"
					}
				}
			}
			observed, evidence := sunsetUsage(usage, row.Method, path)
			row.Requests, row.LastObservedAt, row.Customers, row.Evidence = observed.Requests, observed.LastObservedAt, observed.Customers, evidence
			if row.Customers == nil {
				row.Customers = []api.RouteCustomerObservation{}
			}
			row.AnonymousRequests, row.UnresolvedRequests = observed.AnonymousRequests, observed.UnresolvedIdentityRequests
			if review != nil {
				for _, mapping := range review.Mappings {
					if mapping.From.App != app || mapping.From.Method != row.Method || mapping.From.Path != path {
						continue
					}
					row.ContractStatus = mapping.Status
					for _, pair := range mapping.Successors {
						observedNext, status := sunsetUsage(next[pair.To.App], pair.To.Method, pair.To.Path)
						target := routeSunsetSuccessor{To: pair.To, ContractStatus: pair.Status, Requests: observedNext.Requests, Evidence: status, IdentityComparable: pair.To.App == app}
						identities := map[string]bool{}
						for _, customer := range observedNext.Customers {
							if customer.ConsumerID != "" || customer.PlatformTenantID != "" {
								identities[customer.ConsumerID+"|"+customer.PlatformTenantID] = true
							}
						}
						// Identity equality is meaningful only within the same app.
						if pair.To.App == app {
							for _, customer := range row.Customers {
								if identities[customer.ConsumerID+"|"+customer.PlatformTenantID] {
									target.CallersAlsoObserved++
								}
							}
						}
						row.Successors = append(row.Successors, target)
						if status != "observed_only" {
							row.Findings = append(row.Findings, "incomplete_successor_telemetry")
						}
					}
				}
			}
			if len(row.Successors) == 0 {
				row.Findings = append(row.Findings, "missing_reviewed_successor_mapping")
			}
			report.Routes = append(report.Routes, row)
		}
	}
	priority := map[string]int{"invalid_metadata": 0, "overdue": 1, "upcoming": 2, "unscheduled": 3, "scheduled": 4}
	sort.Slice(report.Routes, func(i, j int) bool {
		a, b := report.Routes[i], report.Routes[j]
		if priority[a.Status] != priority[b.Status] {
			return priority[a.Status] < priority[b.Status]
		}
		if a.SunsetAt != b.SunsetAt {
			return a.SunsetAt < b.SunsetAt
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	return report
}
