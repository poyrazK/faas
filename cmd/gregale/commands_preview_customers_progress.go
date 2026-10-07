package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	previewCustomerMigrationProgressVersion      = 1
	previewCustomerMigrationProgressMaxSnapshots = 20
	previewCustomerMigrationProgressMaxDays      = 3650
)

type previewCustomerMigrationSnapshotPaths []string

func (values *previewCustomerMigrationSnapshotPaths) String() string {
	return strings.Join(*values, ",")
}

func (values *previewCustomerMigrationSnapshotPaths) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type previewCustomerMigrationProgressSummary struct {
	Routes                 int `json:"routes"`
	CustomerRouteLinks     int `json:"customer_route_links"`
	OwnerReviewReadyRoutes int `json:"owner_review_ready_routes"`
	OldRouteActiveRoutes   int `json:"old_route_active_routes"`
	IncompleteRoutes       int `json:"incomplete_routes"`
	SuccessorObservedLinks int `json:"successor_observed_links"`
	NoCurrentEvidenceLinks int `json:"no_current_evidence_links"`
	IncompleteLinks        int `json:"incomplete_customer_links"`
}

type previewCustomerMigrationProgressReport struct {
	Version      int                                        `json:"version"`
	GeneratedAt  time.Time                                  `json:"generated_at"`
	GroupBy      string                                     `json:"group_by"`
	Outcome      string                                     `json:"outcome"`
	GracePeriod  string                                     `json:"grace_period"`
	MinWindows   int                                        `json:"min_windows"`
	MaxStaleness string                                     `json:"max_staleness"`
	Summary      previewCustomerMigrationProgressSummary    `json:"summary"`
	Snapshots    []previewCustomerMigrationProgressSnapshot `json:"snapshots"`
	Routes       []previewCustomerMigrationProgressRoute    `json:"routes"`
	Caveats      []string                                   `json:"caveats"`
}

type previewCustomerMigrationProgressSnapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	From        string    `json:"from,omitempty"`
	Until       string    `json:"until,omitempty"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
}

type previewCustomerMigrationProgressRoute struct {
	From                   previewCustomerMigrationEndpoint           `json:"from"`
	Successors             []previewCustomerMigrationEndpoint         `json:"successors"`
	Status                 string                                     `json:"status"`
	Reason                 string                                     `json:"reason,omitempty"`
	CohortCustomers        int                                        `json:"cohort_customers"`
	SuccessorObserved      int                                        `json:"successor_observed_customers"`
	NoCurrentEvidence      int                                        `json:"no_current_evidence_customers"`
	OldRouteObserved       int                                        `json:"old_route_observed_customers"`
	IncompleteCustomers    int                                        `json:"incomplete_customers"`
	OldRouteTrafficWindows int                                        `json:"old_route_zero_traffic_windows"`
	GracePeriodStart       string                                     `json:"grace_period_start,omitempty"`
	GracePeriodEnd         string                                     `json:"grace_period_end,omitempty"`
	Customers              []previewCustomerMigrationProgressCustomer `json:"customers"`
	Blockers               []string                                   `json:"blockers"`
}

type previewCustomerMigrationProgressCustomer struct {
	ID                      string `json:"id"`
	IdentityScope           string `json:"identity_scope"`
	App                     string `json:"app,omitempty"`
	Status                  string `json:"status"`
	OldRouteObservedWindows int    `json:"old_route_observed_windows"`
	SuccessorWindows        int    `json:"successor_observed_windows"`
}

type previewCustomerMigrationProgressLinkKey struct {
	identity previewCustomerMigrationIdentity
	from     previewCustomerMigrationRouteKey
}

type previewCustomerMigrationProgressRouteState struct {
	from       previewCustomerMigrationEndpoint
	successors []previewCustomerMigrationEndpoint
	links      []previewCustomerMigrationProgressLinkKey
}

type previewCustomerMigrationProgressSnapshotData struct {
	report      previewCustomerMigrationReport
	links       map[previewCustomerMigrationProgressLinkKey]previewCustomerMigrationRoute
	deployments map[string]previewCustomerMigrationAppReport
	generated   time.Time
}

type previewCustomerMigrationProgressWindow struct {
	from  time.Time
	until time.Time
	asOf  time.Time
}

func cmdPreviewCustomersProgress(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-incomplete", "fail-on-not-ready")
	fs := newFlagSet("preview customers progress", flag.ContinueOnError)
	var snapshots previewCustomerMigrationSnapshotPaths
	fs.Var(&snapshots, "snapshot", "saved JSON tracker from gregale preview customers track; repeat per observation")
	gracePeriodFlag := fs.String("grace-period", "30d", "minimum continuous zero-traffic period required for owner review")
	minWindows := fs.Int("min-windows", 2, "minimum distinct complete observation windows")
	maxStalenessFlag := fs.String("max-staleness", "72h", "maximum age of the latest observation window")
	format := fs.String("format", "text", "report format: text or markdown (or use --json)")
	output := fs.String("out", "", "write the machine-readable progress report to a new JSON file")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit nonzero when evidence is incomplete")
	failNotReady := fs.Bool("fail-on-not-ready", false, "exit nonzero unless every route is ready for owner review")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	gracePeriod, graceErr := parsePreviewCustomerMigrationProgressDuration(*gracePeriodFlag)
	maxStaleness, stalenessErr := parsePreviewCustomerMigrationProgressDuration(*maxStalenessFlag)
	if len(positional) != 0 || len(snapshots) < 2 || len(snapshots) > previewCustomerMigrationProgressMaxSnapshots ||
		graceErr != nil || stalenessErr != nil || *minWindows < 2 || *minWindows > previewCustomerMigrationProgressMaxSnapshots || *minWindows > len(snapshots) ||
		!slices.Contains([]string{"text", "markdown"}, *format) || (jsonOutput && *format != "text") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale preview customers progress --snapshot <TRACKER.json> --snapshot <TRACKER.json> [--grace-period 30d] [--min-windows 2] [--max-staleness 72h] [--format text|markdown] [--out <PATH>] [--fail-on-incomplete] [--fail-on-not-ready] [--json]", "preview")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	loaded := make([]previewCustomerMigrationProgressSnapshotData, 0, len(snapshots))
	var totalSnapshotBytes int64
	for i, path := range snapshots {
		report, size, err := readPreviewCustomerMigrationProgressSnapshot(path)
		if err != nil {
			return printErr("Could not read migration snapshot", fmt.Errorf("snapshot %d: %w", i+1, err))
		}
		totalSnapshotBytes += size
		if totalSnapshotBytes > api.RouteImpactReportMaxBytes {
			return printErr("Could not read migration snapshot", errors.New("combined snapshots exceed the 64 MiB limit"))
		}
		data, err := indexPreviewCustomerMigrationProgressSnapshot(report)
		if err != nil {
			return printErr("Invalid migration snapshot", fmt.Errorf("snapshot %d: %w", i+1, err))
		}
		loaded = append(loaded, data)
	}
	report, err := buildPreviewCustomerMigrationProgressReport(loaded, gracePeriod, *gracePeriodFlag, *minWindows, maxStaleness, *maxStalenessFlag, time.Now().UTC())
	if err != nil {
		return printErr("Could not compare migration snapshots", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode migration progress", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save migration progress", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		var rendered bytes.Buffer
		if *format == "markdown" {
			renderPreviewCustomerMigrationProgressMarkdown(&rendered, report)
		} else {
			renderPreviewCustomerMigrationProgressText(&rendered, report)
		}
		if _, err := osStdout.Write(rendered.Bytes()); err != nil {
			return printErr("Could not write migration progress", err)
		}
	}
	if *failIncomplete && report.Outcome == "incomplete" {
		return 1
	}
	if *failNotReady && report.Outcome != "owner_review_ready" {
		return 1
	}
	return 0
}

func parsePreviewCustomerMigrationProgressDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if len(value) > 1 && value[len(value)-1] == 'd' {
		days, err := strconv.ParseInt(value[:len(value)-1], 10, 64)
		if err != nil || days < 1 || days > previewCustomerMigrationProgressMaxDays {
			return 0, errors.New("duration must be between 1 and 3650 days")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 || duration > time.Duration(previewCustomerMigrationProgressMaxDays)*24*time.Hour {
		return 0, errors.New("duration must be positive and no greater than 3650 days")
	}
	return duration, nil
}

func readPreviewCustomerMigrationProgressSnapshot(path string) (previewCustomerMigrationReport, int64, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return previewCustomerMigrationReport{}, 0, errors.New("use a readable regular tracker file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil || int64(len(body)) > api.RouteImpactReportMaxBytes {
		return previewCustomerMigrationReport{}, 0, errors.New("could not read tracker or it exceeds the 64 MiB limit")
	}
	var report previewCustomerMigrationReport
	if err := json.Unmarshal(body, &report); err != nil || report.Version != previewCustomerMigrationVersion {
		return previewCustomerMigrationReport{}, 0, errors.New("snapshot must be version 1 JSON from gregale preview customers track")
	}
	return report, int64(len(body)), nil
}

func indexPreviewCustomerMigrationProgressSnapshot(report previewCustomerMigrationReport) (previewCustomerMigrationProgressSnapshotData, error) {
	if report.Version != previewCustomerMigrationVersion || report.GeneratedAt.IsZero() {
		return previewCustomerMigrationProgressSnapshotData{}, errors.New("tracker version or generated_at is missing")
	}
	if report.GroupBy != "consumer" && report.GroupBy != "tenant" {
		return previewCustomerMigrationProgressSnapshotData{}, errors.New("group_by must be consumer or tenant")
	}
	if len(report.Customers) == 0 || report.Deployments == nil {
		return previewCustomerMigrationProgressSnapshotData{}, errors.New("tracker must include a non-empty customer cohort and deployment list")
	}
	data := previewCustomerMigrationProgressSnapshotData{
		report: report, generated: report.GeneratedAt.UTC(),
		links:       make(map[previewCustomerMigrationProgressLinkKey]previewCustomerMigrationRoute),
		deployments: make(map[string]previewCustomerMigrationAppReport),
	}
	appSet := map[string]bool{}
	for i, deployment := range report.Deployments {
		if !validCLISlug(deployment.App) || !canonicalRouteHealthID(deployment.DeploymentID) {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("deployment %d has an invalid app or immutable deployment ID", i+1)
		}
		if deployment.Status != "available" && deployment.Status != "incomplete" && deployment.Status != "unavailable" {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("deployment %s has an unsupported evidence status", deployment.App)
		}
		if deployment.Status == "available" && deployment.Coverage != "observed_only" {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("deployment %s has unsupported route telemetry coverage", deployment.App)
		}
		if _, exists := data.deployments[deployment.App]; exists {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("deployment for app %s appears more than once", deployment.App)
		}
		data.deployments[deployment.App] = deployment
		appSet[deployment.App] = true
	}
	if len(data.deployments) == 0 {
		return previewCustomerMigrationProgressSnapshotData{}, errors.New("tracker has no deployment evidence")
	}
	seenCustomers := map[previewCustomerMigrationIdentity]bool{}
	routeSuccessors := map[previewCustomerMigrationRouteKey][]previewCustomerMigrationEndpoint{}
	for i, customer := range report.Customers {
		identity := previewCustomerMigrationIdentity{id: customer.ID, app: customer.App}
		id, err := uuid.Parse(customer.ID)
		if err != nil || id == uuid.Nil || id.String() != customer.ID || seenCustomers[identity] {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %d has a missing, non-canonical, or duplicate identity", i+1)
		}
		if report.GroupBy == "consumer" {
			if customer.IdentityScope != "app" || !validCLISlug(customer.App) {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("consumer %s must have app-scoped identity", customer.ID)
			}
		} else if customer.IdentityScope != "account" || customer.App != "" {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("tenant %s must have account-scoped identity", customer.ID)
		}
		seenCustomers[identity] = true
		if len(customer.Routes) == 0 {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s has no route links", customer.ID)
		}
		for j, route := range customer.Routes {
			from, err := normalizePreviewCustomerMigrationEndpoint(route.From)
			if err != nil {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route %d has an invalid old route", customer.ID, j+1)
			}
			if report.GroupBy == "consumer" && customer.App != from.App {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("consumer %s route app does not match its identity scope", customer.ID)
			}
			validOldTotalEvidence := route.OldRouteTotalEvidence == "" || route.OldRouteTotalEvidence == "observed" || route.OldRouteTotalEvidence == "not_observed" || route.OldRouteTotalEvidence == "incomplete"
			if route.OldRouteRequests < 0 || route.OldRouteTotalRequests < 0 || !validCustomerRouteEvidence(route.OldRouteEvidence) ||
				!validMigrationSuccessorEvidence(route.SuccessorEvidence) || !validOldTotalEvidence {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route %s %s has invalid evidence fields", customer.ID, from.Method, from.Path)
			}
			if route.OldRouteEvidence == "observed" && route.OldRouteRequests <= 0 ||
				route.OldRouteEvidence == "not_observed" && route.OldRouteRequests != 0 {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route %s %s has contradictory customer evidence", customer.ID, from.Method, from.Path)
			}
			if route.OldRouteTotalEvidence == "observed" && route.OldRouteTotalRequests <= 0 ||
				route.OldRouteTotalEvidence == "not_observed" && route.OldRouteTotalRequests != 0 {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route %s %s has contradictory total-traffic evidence", customer.ID, from.Method, from.Path)
			}
			if route.Status != "old_route_active" && route.Status != "successor_observed" && route.Status != "both" &&
				route.Status != "no_current_evidence" && route.Status != "in_place_unmeasurable" && route.Status != "incomplete" {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route %s %s has an unsupported status", customer.ID, from.Method, from.Path)
			}
			successors := make([]previewCustomerMigrationEndpoint, 0, len(route.Successors))
			successorSeen := map[previewCustomerMigrationRouteKey]bool{}
			for _, value := range route.Successors {
				successor, err := normalizePreviewCustomerMigrationEndpoint(value)
				if err != nil {
					return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route has an invalid successor", customer.ID)
				}
				if report.GroupBy == "consumer" && successor.App != customer.App {
					return previewCustomerMigrationProgressSnapshotData{}, errors.New("consumer identities cannot be correlated across apps")
				}
				key := previewCustomerMigrationEndpointKey(successor)
				if successorSeen[key] {
					return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route repeats a successor", customer.ID)
				}
				successorSeen[key] = true
				successors = append(successors, successor)
				appSet[successor.App] = true
			}
			if route.ObservedSuccessors == nil && route.SuccessorEvidence == "observed" {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route claims a successor observation without route details", customer.ID)
			}
			observedSuccessors := map[previewCustomerMigrationRouteKey]bool{}
			for _, observed := range route.ObservedSuccessors {
				endpoint, err := normalizePreviewCustomerMigrationEndpoint(observed.Route)
				key := previewCustomerMigrationEndpointKey(endpoint)
				if err != nil || !successorSeen[key] || observedSuccessors[key] || observed.Requests <= 0 {
					return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route has an invalid observed successor detail", customer.ID)
				}
				observedSuccessors[key] = true
			}
			if (route.SuccessorEvidence == "observed") != (len(route.ObservedSuccessors) > 0) ||
				(len(successors) == 0 && route.SuccessorEvidence != "not_mapped") ||
				(len(successors) > 0 && route.SuccessorEvidence == "not_mapped") {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s route has contradictory successor evidence", customer.ID)
			}
			sort.Slice(successors, func(i, j int) bool { return migrationEndpointLess(successors[i], successors[j]) })
			key := previewCustomerMigrationProgressLinkKey{identity: identity, from: previewCustomerMigrationEndpointKey(from)}
			if _, exists := data.links[key]; exists {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer %s repeats route %s %s", customer.ID, from.Method, from.Path)
			}
			if previous, exists := routeSuccessors[key.from]; exists && !slices.Equal(previous, successors) {
				return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("customer links for %s %s use different successor mappings", from.Method, from.Path)
			}
			routeSuccessors[key.from] = append([]previewCustomerMigrationEndpoint{}, successors...)
			route.From, route.Successors = from, successors
			data.links[key] = route
		}
	}
	if len(data.links) == 0 || len(data.links) > previewCustomerMigrationMaxLinks {
		return previewCustomerMigrationProgressSnapshotData{}, errors.New("tracker has no route links or exceeds the supported cohort limit")
	}
	expectedApps := map[string]bool{}
	for key, route := range data.links {
		expectedApps[key.from.app] = true
		for _, successor := range route.Successors {
			expectedApps[successor.App] = true
		}
	}
	if len(expectedApps) != len(appSet) {
		return previewCustomerMigrationProgressSnapshotData{}, errors.New("deployment list does not match the apps in the cohort and successor mappings")
	}
	for app := range expectedApps {
		if !appSet[app] {
			return previewCustomerMigrationProgressSnapshotData{}, fmt.Errorf("tracker is missing deployment evidence for app %s", app)
		}
	}
	return data, nil
}

func validCustomerRouteEvidence(value string) bool {
	return value == "observed" || value == "not_observed" || value == "incomplete"
}

func validMigrationSuccessorEvidence(value string) bool {
	return validCustomerRouteEvidence(value) || value == "not_mapped"
}

func migrationEndpointLess(a, b previewCustomerMigrationEndpoint) bool {
	if a.App != b.App {
		return a.App < b.App
	}
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	return a.Path < b.Path
}

func buildPreviewCustomerMigrationProgressReport(
	snapshots []previewCustomerMigrationProgressSnapshotData,
	gracePeriod time.Duration,
	gracePeriodLabel string,
	minWindows int,
	maxStaleness time.Duration,
	maxStalenessLabel string,
	now time.Time,
) (previewCustomerMigrationProgressReport, error) {
	if len(snapshots) < 2 || len(snapshots) > previewCustomerMigrationProgressMaxSnapshots {
		return previewCustomerMigrationProgressReport{}, fmt.Errorf("supply between 2 and %d tracker snapshots", previewCustomerMigrationProgressMaxSnapshots)
	}
	if gracePeriod <= 0 || maxStaleness <= 0 || minWindows < 2 || minWindows > len(snapshots) {
		return previewCustomerMigrationProgressReport{}, errors.New("grace period, max staleness, and minimum window count are invalid")
	}
	first := snapshots[0]
	for _, snapshot := range snapshots[1:] {
		if err := comparePreviewCustomerMigrationProgressSnapshots(first, snapshot); err != nil {
			return previewCustomerMigrationProgressReport{}, err
		}
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].generated.Before(snapshots[j].generated) })
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i].generated.Equal(snapshots[i-1].generated) {
			return previewCustomerMigrationProgressReport{}, errors.New("snapshot generated_at timestamps must be distinct")
		}
	}
	if err := rejectDuplicateMigrationProgressWindows(snapshots); err != nil {
		return previewCustomerMigrationProgressReport{}, err
	}
	report := previewCustomerMigrationProgressReport{
		Version: previewCustomerMigrationProgressVersion, GeneratedAt: now.UTC(), GroupBy: first.report.GroupBy, Outcome: "owner_review_ready",
		GracePeriod: gracePeriodLabel, MinWindows: minWindows, MaxStaleness: maxStalenessLabel,
		Snapshots: []previewCustomerMigrationProgressSnapshot{}, Routes: []previewCustomerMigrationProgressRoute{},
		Caveats: []string{
			"Owner-review readiness means complete old-route telemetry showed zero aggregate requests to that route over the stated continuous period. It is an advisory checkpoint, not proof that removing the route is safe.",
			"Telemetry can be sampled, delayed, anonymous, or unresolved. Customer-level silence does not prove migration; inspect each no-current-evidence row and confirm product and support requirements with the route owner.",
			"Use the same immutable deployment IDs and unchanged cohort and successor mapping in every snapshot. A new release or mapping change starts a new evidence series.",
		},
	}
	for _, snapshot := range snapshots {
		window, reason := previewCustomerMigrationProgressWindowForApps(snapshot, sortedMigrationProgressAppNames(snapshot.deployments))
		row := previewCustomerMigrationProgressSnapshot{GeneratedAt: snapshot.generated}
		if reason == "" {
			row.From, row.Until, row.Status = window.from.Format(time.RFC3339Nano), window.until.Format(time.RFC3339Nano), "complete"
		} else {
			row.Status, row.Reason = "incomplete", reason
		}
		report.Snapshots = append(report.Snapshots, row)
	}
	routes := groupPreviewCustomerMigrationProgressRoutes(first.links)
	for _, routeState := range routes {
		row := evaluatePreviewCustomerMigrationProgressRoute(routeState, snapshots, gracePeriod, minWindows, maxStaleness, now.UTC())
		report.Routes = append(report.Routes, row)
		report.Summary.Routes++
		report.Summary.CustomerRouteLinks += len(row.Customers)
		switch row.Status {
		case "owner_review_ready":
			report.Summary.OwnerReviewReadyRoutes++
		case "old_route_active":
			report.Summary.OldRouteActiveRoutes++
		case "incomplete", "insufficient_observation":
			report.Summary.IncompleteRoutes++
		}
		for _, customer := range row.Customers {
			switch customer.Status {
			case "successor_observed":
				report.Summary.SuccessorObservedLinks++
			case "no_current_evidence":
				report.Summary.NoCurrentEvidenceLinks++
			case "incomplete", "in_place_unmeasurable":
				report.Summary.IncompleteLinks++
			}
		}
	}
	if report.Summary.OwnerReviewReadyRoutes != report.Summary.Routes {
		report.Outcome = "review_required"
	}
	if report.Summary.IncompleteRoutes > 0 || report.Summary.IncompleteLinks > 0 {
		report.Outcome = "incomplete"
	}
	return report, nil
}

func comparePreviewCustomerMigrationProgressSnapshots(first, next previewCustomerMigrationProgressSnapshotData) error {
	if first.report.GroupBy != next.report.GroupBy {
		return errors.New("snapshots use different customer identity scopes")
	}
	if len(first.links) != len(next.links) {
		return errors.New("snapshots do not contain the same customer-route cohort")
	}
	if len(first.deployments) != len(next.deployments) {
		return errors.New("snapshots use different deployment sets")
	}
	for app, deployment := range first.deployments {
		other, ok := next.deployments[app]
		if !ok || deployment.DeploymentID != other.DeploymentID {
			return fmt.Errorf("snapshots must use the same immutable deployment for app %s", app)
		}
	}
	for key, route := range first.links {
		other, ok := next.links[key]
		if !ok || len(route.Successors) != len(other.Successors) {
			return errors.New("snapshots do not contain the same customer-route cohort and successor mapping")
		}
		for i := range route.Successors {
			if route.Successors[i] != other.Successors[i] {
				return errors.New("snapshots use different successor mappings")
			}
		}
	}
	return nil
}

func rejectDuplicateMigrationProgressWindows(snapshots []previewCustomerMigrationProgressSnapshotData) error {
	seen := map[string]bool{}
	for _, snapshot := range snapshots {
		apps := sortedMigrationProgressAppNames(snapshot.deployments)
		var key strings.Builder
		for _, app := range apps {
			deployment := snapshot.deployments[app]
			from, fromErr := time.Parse(time.RFC3339Nano, deployment.From)
			until, untilErr := time.Parse(time.RFC3339Nano, deployment.Until)
			key.WriteString(app)
			key.WriteByte('\x00')
			if fromErr == nil {
				key.WriteString(from.UTC().Format(time.RFC3339Nano))
			} else {
				key.WriteString(deployment.From)
			}
			key.WriteByte('\x00')
			if untilErr == nil {
				key.WriteString(until.UTC().Format(time.RFC3339Nano))
			} else {
				key.WriteString(deployment.Until)
			}
			key.WriteByte('\x00')
		}
		if seen[key.String()] {
			return errors.New("snapshots include a duplicate observation window; each snapshot must add distinct time coverage")
		}
		seen[key.String()] = true
	}
	return nil
}

func sortedMigrationProgressAppNames(values map[string]previewCustomerMigrationAppReport) []string {
	result := make([]string, 0, len(values))
	for app := range values {
		result = append(result, app)
	}
	sort.Strings(result)
	return result
}

func groupPreviewCustomerMigrationProgressRoutes(links map[previewCustomerMigrationProgressLinkKey]previewCustomerMigrationRoute) []previewCustomerMigrationProgressRouteState {
	groups := map[previewCustomerMigrationRouteKey]*previewCustomerMigrationProgressRouteState{}
	for key, route := range links {
		group := groups[key.from]
		if group == nil {
			group = &previewCustomerMigrationProgressRouteState{from: route.From, successors: append([]previewCustomerMigrationEndpoint{}, route.Successors...)}
			groups[key.from] = group
		}
		group.links = append(group.links, key)
	}
	result := make([]previewCustomerMigrationProgressRouteState, 0, len(groups))
	for _, group := range groups {
		sort.Slice(group.links, func(i, j int) bool {
			a, b := group.links[i].identity, group.links[j].identity
			if a.app != b.app {
				return a.app < b.app
			}
			return a.id < b.id
		})
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool { return migrationEndpointLess(result[i].from, result[j].from) })
	return result
}

func evaluatePreviewCustomerMigrationProgressRoute(
	route previewCustomerMigrationProgressRouteState,
	snapshots []previewCustomerMigrationProgressSnapshotData,
	gracePeriod time.Duration,
	minWindows int,
	maxStaleness time.Duration,
	now time.Time,
) previewCustomerMigrationProgressRoute {
	row := previewCustomerMigrationProgressRoute{
		From: route.from, Successors: append([]previewCustomerMigrationEndpoint{}, route.successors...),
		Status: "incomplete", Customers: []previewCustomerMigrationProgressCustomer{}, Blockers: []string{},
	}
	for _, key := range route.links {
		scope := "account"
		if snapshots[0].report.GroupBy == "consumer" {
			scope = "app"
		}
		row.Customers = append(row.Customers, previewCustomerMigrationProgressCustomer{ID: key.identity.id, App: key.identity.app, IdentityScope: scope})
	}
	row.CohortCustomers = len(row.Customers)

	candidates := []previewCustomerMigrationProgressWindow{}
	oldObservedLatest := false
	latestReason := "insufficient_complete_observation_windows"
	for _, snapshot := range snapshots {
		totalEvidence, totalRequests, totalConsistent := migrationProgressRouteTotal(snapshot, route.links)
		if !totalConsistent || totalEvidence == "" || totalEvidence == "incomplete" {
			candidates = nil
			latestReason = "aggregate_old_route_traffic_evidence_unavailable"
			oldObservedLatest = false
			continue
		}
		if totalEvidence == "observed" || totalRequests > 0 {
			candidates = nil
			latestReason = "old_route_traffic_observed"
			oldObservedLatest = true
			continue
		}
		oldObservedLatest = false
		window, reason := previewCustomerMigrationProgressWindowForApps(snapshot, []string{route.from.App})
		if reason != "" {
			candidates = nil
			latestReason = reason
			continue
		}
		if len(candidates) > 0 && window.from.After(candidates[len(candidates)-1].until) {
			candidates = nil
			latestReason = "observation_window_gap"
		}
		if len(candidates) > 0 && window.until.Before(candidates[len(candidates)-1].until) {
			candidates = nil
			latestReason = "observation_windows_out_of_order"
		}
		candidates = append(candidates, window)
		latestReason = "grace_period_not_elapsed"
	}
	if len(candidates) > 0 {
		firstWindow := candidates[0]
		lastWindow := candidates[len(candidates)-1]
		span := lastWindow.until.Sub(firstWindow.from)
		row.OldRouteTrafficWindows = len(candidates)
		row.GracePeriodStart = firstWindow.from.Format(time.RFC3339Nano)
		row.GracePeriodEnd = lastWindow.until.Format(time.RFC3339Nano)
		if span < 0 {
			span = 0
		}
		if len(candidates) >= minWindows && span >= gracePeriod {
			age := now.Sub(lastWindow.asOf)
			if age < 0 {
				age = 0
			}
			if age <= maxStaleness {
				row.Status, row.Reason = "owner_review_ready", ""
			} else {
				latestReason = "latest_observation_is_stale"
			}
		} else if len(candidates) < minWindows {
			latestReason = "insufficient_complete_observation_windows"
		} else {
			latestReason = "grace_period_not_elapsed"
		}
	}
	if row.Status != "owner_review_ready" {
		if oldObservedLatest {
			row.Status = "old_route_active"
		} else if strings.Contains(latestReason, "unavailable") || strings.Contains(latestReason, "incomplete") || strings.Contains(latestReason, "gap") || strings.Contains(latestReason, "out_of_order") || latestReason == "latest_observation_is_stale" {
			row.Status = "incomplete"
		} else {
			row.Status = "insufficient_observation"
		}
		row.Reason = latestReason
		row.Blockers = append(row.Blockers, latestReason)
	}

	qualifyingFrom, qualifyingUntil := time.Time{}, time.Time{}
	if row.Status == "owner_review_ready" {
		qualifyingFrom, qualifyingUntil = candidates[0].from, candidates[len(candidates)-1].until
	}
	for i, key := range route.links {
		customer := &row.Customers[i]
		oldObserved, successorObserved := 0, 0
		for _, snapshot := range snapshots {
			link := snapshot.links[key]
			if link.OldRouteEvidence == "observed" {
				oldObserved++
			}
			if link.SuccessorEvidence == "observed" {
				successorObserved++
			}
		}
		customer.OldRouteObservedWindows = oldObserved
		customer.SuccessorWindows = successorObserved
		if row.Status == "owner_review_ready" && !qualifyingFrom.IsZero() {
			customer.OldRouteObservedWindows = migrationProgressCountEvidenceWindows(snapshots, key, qualifyingFrom, qualifyingUntil, "old")
			customer.SuccessorWindows = migrationProgressCountEvidenceWindows(snapshots, key, qualifyingFrom, qualifyingUntil, "successor")
		}
		latest := snapshots[len(snapshots)-1].links[key]
		switch {
		case latest.OldRouteEvidence == "observed":
			customer.Status = "old_route_active"
		case customer.OldRouteObservedWindows > 0:
			customer.Status = "old_route_observed"
		case latest.Status == "in_place_unmeasurable":
			customer.Status = "in_place_unmeasurable"
		case latest.Status == "incomplete" || latest.BaselineIncomplete || latest.OldRouteEvidence == "incomplete":
			customer.Status = "incomplete"
		case customer.SuccessorWindows > 0:
			customer.Status = "successor_observed"
		default:
			customer.Status = "no_current_evidence"
		}
		switch customer.Status {
		case "old_route_active", "old_route_observed":
			row.OldRouteObserved++
		case "successor_observed":
			row.SuccessorObserved++
		case "incomplete", "in_place_unmeasurable":
			row.IncompleteCustomers++
		default:
			row.NoCurrentEvidence++
		}
	}
	return row
}

func migrationProgressRouteTotal(snapshot previewCustomerMigrationProgressSnapshotData, links []previewCustomerMigrationProgressLinkKey) (string, int64, bool) {
	var evidence string
	var requests int64
	for i, key := range links {
		route, ok := snapshot.links[key]
		if !ok {
			return "", 0, false
		}
		if i == 0 {
			evidence, requests = route.OldRouteTotalEvidence, route.OldRouteTotalRequests
			continue
		}
		if route.OldRouteTotalEvidence != evidence || route.OldRouteTotalRequests != requests {
			return "", 0, false
		}
	}
	return evidence, requests, len(links) > 0
}

func previewCustomerMigrationProgressWindowForApps(snapshot previewCustomerMigrationProgressSnapshotData, apps []string) (previewCustomerMigrationProgressWindow, string) {
	var result previewCustomerMigrationProgressWindow
	for i, app := range apps {
		deployment, ok := snapshot.deployments[app]
		if !ok {
			return previewCustomerMigrationProgressWindow{}, "deployment_evidence_missing"
		}
		if deployment.Status != "available" || deployment.WindowClamped || deployment.RoutesTruncated {
			return previewCustomerMigrationProgressWindow{}, "telemetry_window_incomplete"
		}
		from, fromErr := time.Parse(time.RFC3339Nano, deployment.From)
		until, untilErr := time.Parse(time.RFC3339Nano, deployment.Until)
		asOf, asOfErr := time.Parse(time.RFC3339Nano, deployment.AsOf)
		if fromErr != nil || untilErr != nil || asOfErr != nil || !until.After(from) {
			return previewCustomerMigrationProgressWindow{}, "observation_window_timestamps_invalid"
		}
		if i == 0 {
			result = previewCustomerMigrationProgressWindow{from: from, until: until, asOf: asOf}
			continue
		}
		if !from.Equal(result.from) || !until.Equal(result.until) {
			return previewCustomerMigrationProgressWindow{}, "cross_app_observation_window_mismatch"
		}
		if asOf.Before(result.asOf) {
			result.asOf = asOf
		}
	}
	if len(apps) == 0 {
		return previewCustomerMigrationProgressWindow{}, "deployment_evidence_missing"
	}
	return result, ""
}

func migrationProgressCountEvidenceWindows(
	snapshots []previewCustomerMigrationProgressSnapshotData,
	key previewCustomerMigrationProgressLinkKey,
	from, until time.Time,
	kind string,
) int {
	count := 0
	for _, snapshot := range snapshots {
		window, reason := previewCustomerMigrationProgressWindowForApps(snapshot, []string{key.from.app})
		if reason != "" || window.from.Before(from) || window.until.After(until) {
			continue
		}
		route := snapshot.links[key]
		if kind == "old" && route.OldRouteEvidence == "observed" || kind == "successor" && route.SuccessorEvidence == "observed" {
			count++
		}
	}
	return count
}

func renderPreviewCustomerMigrationProgressText(w io.Writer, report previewCustomerMigrationProgressReport) {
	_, _ = fmt.Fprintf(w, "Route migration progress (%s)\nOutcome: %s; routes ready for owner review: %d/%d; grace period: %s; minimum windows: %d; max staleness: %s\n",
		report.GroupBy, report.Outcome, report.Summary.OwnerReviewReadyRoutes, report.Summary.Routes, report.GracePeriod, report.MinWindows, report.MaxStaleness)
	for _, snapshot := range report.Snapshots {
		_, _ = fmt.Fprintf(w, "Snapshot %s: %s", snapshot.GeneratedAt.Format(time.RFC3339), snapshot.Status)
		if snapshot.From != "" {
			_, _ = fmt.Fprintf(w, "; observed %s to %s", snapshot.From, snapshot.Until)
		}
		if snapshot.Reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", snapshot.Reason)
		}
		_, _ = fmt.Fprintln(w)
	}
	for _, route := range report.Routes {
		_, _ = fmt.Fprintf(w, "\n%s %s in %s: %s", route.From.Method, previewSourceDisplay(route.From.Path, false), route.From.App, route.Status)
		_, _ = fmt.Fprintf(w, "; successors: %s", renderPreviewCustomerMigrationProgressSuccessors(route.Successors, false))
		if route.GracePeriodStart != "" {
			_, _ = fmt.Fprintf(w, "; zero aggregate traffic observed across %d windows from %s to %s", route.OldRouteTrafficWindows, route.GracePeriodStart, route.GracePeriodEnd)
		}
		if route.Reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", route.Reason)
		}
		_, _ = fmt.Fprintln(w)
		for _, customer := range route.Customers {
			label := previewSourceDisplay(customer.ID, false)
			if customer.App != "" {
				label += " (app " + previewSourceDisplay(customer.App, false) + ")"
			}
			_, _ = fmt.Fprintf(w, "  %s: %s; old route observed in %d windows; successor observed in %d windows\n",
				label, customer.Status, customer.OldRouteObservedWindows, customer.SuccessorWindows)
		}
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "Note: %s\n", caveat)
	}
}

func renderPreviewCustomerMigrationProgressMarkdown(w io.Writer, report previewCustomerMigrationProgressReport) {
	_, _ = fmt.Fprintf(w, "## Route migration progress\n\nOutcome: **%s**. Routes ready for owner review: **%d/%d**. Grace period: **%s**; minimum complete windows: **%d**; maximum staleness: **%s**.\n\n",
		report.Outcome, report.Summary.OwnerReviewReadyRoutes, report.Summary.Routes, report.GracePeriod, report.MinWindows, report.MaxStaleness)
	_, _ = fmt.Fprintln(w, "| Old route | Successors | Status | Zero-traffic windows | Evidence span | Customers: successor observed / no current evidence / incomplete | Reason |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---:|---|---:|---|")
	for _, route := range report.Routes {
		span := ""
		if route.GracePeriodStart != "" {
			span = route.GracePeriodStart + " to " + route.GracePeriodEnd
		}
		_, _ = fmt.Fprintf(w, "| `%s %s` in `%s` | %s | **%s** | %d | %s | %d / %d / %d | %s |\n",
			route.From.Method, previewSourceDisplay(route.From.Path, true), route.From.App,
			renderPreviewCustomerMigrationProgressSuccessors(route.Successors, true), route.Status, route.OldRouteTrafficWindows,
			previewSourceDisplay(span, true), route.SuccessorObserved, route.NoCurrentEvidence, route.IncompleteCustomers,
			previewSourceDisplay(route.Reason, true))
	}
	_, _ = fmt.Fprintln(w)
	for _, route := range report.Routes {
		_, _ = fmt.Fprintf(w, "### `%s %s` in `%s`\n\n", route.From.Method, previewSourceDisplay(route.From.Path, true), route.From.App)
		_, _ = fmt.Fprintln(w, "| Customer ID | Scope | Progress | Old-route windows | Successor windows |")
		_, _ = fmt.Fprintln(w, "|---|---|---|---:|---:|")
		for _, customer := range route.Customers {
			_, _ = fmt.Fprintf(w, "| %s | %s | **%s** | %d | %d |\n", previewSourceDisplay(customer.ID, true),
				previewSourceDisplay(customer.IdentityScope, true), customer.Status, customer.OldRouteObservedWindows, customer.SuccessorWindows)
		}
		_, _ = fmt.Fprintln(w)
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "> %s\n\n", caveat)
	}
}

func renderPreviewCustomerMigrationProgressSuccessors(values []previewCustomerMigrationEndpoint, markdown bool) string {
	if len(values) == 0 {
		return "none mapped"
	}
	labels := make([]string, 0, len(values))
	for _, endpoint := range values {
		label := endpoint.App + " " + endpoint.Method + " " + previewSourceDisplay(endpoint.Path, markdown)
		labels = append(labels, label)
	}
	return strings.Join(labels, "; ")
}
