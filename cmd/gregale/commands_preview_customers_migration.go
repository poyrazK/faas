package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const previewCustomerCutoverReviewVersion = 1

type previewCustomerCutoverReviewReport struct {
	Version                   int                                        `json:"version"`
	GeneratedAt               time.Time                                  `json:"generated_at"`
	Outcome                   string                                     `json:"outcome"`
	OwnerReviewRequired       bool                                       `json:"owner_review_required"`
	GroupBy                   string                                     `json:"group_by"`
	ContractReviewGeneratedAt time.Time                                  `json:"contract_review_generated_at"`
	ContractReviewOutcome     string                                     `json:"contract_review_outcome"`
	FromDeployments           []routeMigrationDeploymentEvidence         `json:"from_deployments"`
	ToDeployments             []routeMigrationDeploymentEvidence         `json:"to_deployments"`
	GracePeriod               string                                     `json:"grace_period"`
	MinWindows                int                                        `json:"min_windows"`
	MaxStaleness              string                                     `json:"max_staleness"`
	Summary                   previewCustomerCutoverReviewSummary        `json:"summary"`
	Snapshots                 []previewCustomerMigrationProgressSnapshot `json:"snapshots"`
	Routes                    []previewCustomerCutoverReviewRoute        `json:"routes"`
	Caveats                   []string                                   `json:"caveats"`
}

type previewCustomerCutoverReviewSummary struct {
	Routes                          int `json:"routes"`
	CustomerRouteLinks              int `json:"customer_route_links"`
	OwnerReviewReadyRoutes          int `json:"owner_review_ready_routes"`
	BreakingContractRoutes          int `json:"breaking_contract_routes"`
	ContractReviewRequiredRoutes    int `json:"contract_review_required_routes"`
	ContractUnknownRoutes           int `json:"contract_unknown_routes"`
	OldRouteActiveRoutes            int `json:"old_route_active_routes"`
	TelemetryIncompleteRoutes       int `json:"telemetry_incomplete_routes"`
	SuccessorObservedLinks          int `json:"successor_observed_customer_links"`
	OldRouteObservedLinks           int `json:"old_route_observed_customer_links"`
	NoCurrentEvidenceLinks          int `json:"no_current_evidence_customer_links"`
	IncompleteCustomerLinks         int `json:"incomplete_customer_links"`
	CustomersOnBreakingSuccessors   int `json:"customers_on_breaking_successors"`
	CustomersOnReviewSuccessors     int `json:"customers_on_review_required_successors"`
	CustomersOnUnknownSuccessors    int `json:"customers_on_unknown_successors"`
	CustomersOnCompatibleSuccessors int `json:"customers_on_compatible_successors"`
	ObservedOldRouteReuseLinks      int `json:"observed_old_route_reuse_customer_links"`
	LatestBreakingSuccessorLinks    int `json:"latest_breaking_successor_customer_links"`
	LatestReviewSuccessorLinks      int `json:"latest_review_required_successor_customer_links"`
	LatestUnknownSuccessorLinks     int `json:"latest_unknown_successor_customer_links"`
	LatestCompatibleSuccessorLinks  int `json:"latest_compatible_successor_customer_links"`
	AmbiguousLatestSuccessorLinks   int `json:"ambiguous_latest_successor_customer_links"`
}

type previewCustomerCutoverReviewRoute struct {
	From                   previewCustomerMigrationEndpoint        `json:"from"`
	Successors             []previewCustomerCutoverReviewSuccessor `json:"successors"`
	ContractStatus         string                                  `json:"contract_status"`
	TelemetryStatus        string                                  `json:"telemetry_status"`
	Status                 string                                  `json:"status"`
	Reason                 string                                  `json:"reason,omitempty"`
	CohortCustomers        int                                     `json:"cohort_customers"`
	SuccessorObserved      int                                     `json:"successor_observed_customers"`
	NoCurrentEvidence      int                                     `json:"no_current_evidence_customers"`
	OldRouteObserved       int                                     `json:"old_route_observed_customers"`
	IncompleteCustomers    int                                     `json:"incomplete_customers"`
	OldRouteTrafficWindows int                                     `json:"old_route_zero_traffic_windows"`
	GracePeriodStart       string                                  `json:"grace_period_start,omitempty"`
	GracePeriodEnd         string                                  `json:"grace_period_end,omitempty"`
	Customers              []previewCustomerCutoverReviewCustomer  `json:"customers"`
	Blockers               []string                                `json:"blockers"`
}

type previewCustomerCutoverReviewSuccessor struct {
	To       previewCustomerMigrationEndpoint `json:"to"`
	Status   string                           `json:"contract_status"`
	Findings []routeMigrationFindingSummary   `json:"findings"`
}

type routeMigrationFindingSummary struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Location string `json:"location,omitempty"`
}

type previewCustomerCutoverReviewCustomer struct {
	ID                      string                                       `json:"id"`
	IdentityScope           string                                       `json:"identity_scope"`
	App                     string                                       `json:"app,omitempty"`
	MigrationEvidence       string                                       `json:"migration_evidence"`
	NextStep                string                                       `json:"next_step"`
	OldRouteObservedWindows int                                          `json:"old_route_observed_windows"`
	SuccessorWindows        int                                          `json:"successor_observed_windows"`
	ObservedSuccessors      []previewCustomerCutoverObservedSuccessor    `json:"observed_successors"`
	LatestSuccessorEvidence string                                       `json:"latest_successor_evidence"`
	LatestSuccessors        []previewCustomerCutoverLatestSuccessor      `json:"latest_observed_successors"`
	ObservedOldRouteReuse   *previewCustomerCutoverOldRouteReuse         `json:"observed_old_route_reuse,omitempty"`
	OldRouteObservations    []previewCustomerCutoverSuccessorObservation `json:"old_route_observations,omitempty"`
}

type previewCustomerCutoverLatestSuccessor struct {
	Route               previewCustomerMigrationEndpoint `json:"route"`
	ContractStatus      string                           `json:"contract_status"`
	Requests            int64                            `json:"observed_requests"`
	LastObservedAt      time.Time                        `json:"last_observed_at"`
	SnapshotGeneratedAt time.Time                        `json:"snapshot_generated_at"`
	WindowFrom          time.Time                        `json:"window_from"`
	WindowUntil         time.Time                        `json:"window_until"`
}

type previewCustomerCutoverOldRouteReuse struct {
	Signal                       string                           `json:"signal"`
	Successor                    previewCustomerMigrationEndpoint `json:"successor"`
	SuccessorContractStatus      string                           `json:"successor_contract_status"`
	SuccessorLastObservedAt      time.Time                        `json:"successor_last_observed_at"`
	SuccessorSnapshotGeneratedAt time.Time                        `json:"successor_snapshot_generated_at"`
	SuccessorWindowFrom          time.Time                        `json:"successor_window_from"`
	SuccessorWindowUntil         time.Time                        `json:"successor_window_until"`
	OldRouteLastObservedAt       time.Time                        `json:"old_route_last_observed_at"`
	OldRouteSnapshotGeneratedAt  time.Time                        `json:"old_route_snapshot_generated_at"`
	OldRouteWindowFrom           time.Time                        `json:"old_route_window_from"`
	OldRouteWindowUntil          time.Time                        `json:"old_route_window_until"`
}

type previewCustomerCutoverObservedSuccessor struct {
	Route          previewCustomerMigrationEndpoint             `json:"route"`
	ContractStatus string                                       `json:"contract_status"`
	Observations   []previewCustomerCutoverSuccessorObservation `json:"observations"`
}

type previewCustomerCutoverSuccessorObservation struct {
	SnapshotGeneratedAt time.Time `json:"snapshot_generated_at"`
	WindowFrom          string    `json:"window_from,omitempty"`
	WindowUntil         string    `json:"window_until,omitempty"`
	Requests            int64     `json:"observed_requests"`
	LastObservedAt      string    `json:"last_observed_at,omitempty"`
}

type previewCustomerCutoverReviewSnapshotPaths []string

func (values *previewCustomerCutoverReviewSnapshotPaths) String() string {
	return strings.Join(*values, ",")
}

func (values *previewCustomerCutoverReviewSnapshotPaths) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func cmdPreviewCustomersMigration(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale preview customers migration <review|diff> [options]", "preview")
		return 1
	}
	switch args[0] {
	case "review":
		return cmdPreviewCustomersMigrationReview(args[1:])
	case "diff":
		return cmdPreviewCustomersMigrationDiff(args[1:])
	default:
		PrintUsage(osStderr, "usage: gregale preview customers migration <review|diff> [options]", "preview")
		return 1
	}
}

func cmdPreviewCustomersMigrationReview(args []string) int {
	return cmdRouteMigrationCutoverReview(args, false)
}

// Both entry points use the same evidence validation and readiness rules. The
// routes entry point refreshes contracts instead of accepting a saved review.
func cmdRouteMigrationCutoverReview(args []string, refreshContracts bool) int {
	flags, positional := splitArgsForFlags(args, "fail-on-breaking", "fail-on-incomplete", "fail-on-not-ready")
	command, docsTopic := "preview customers migration review", "preview"
	contractUsage := "--contract-review <PATH>"
	if refreshContracts {
		command, docsTopic = "routes migration readiness", "cli"
		contractUsage = "--mapping <PATH> --from-deployment APP=ID --to-deployment APP=ID"
	}
	fs := newFlagSet(command, flag.ContinueOnError)
	var contractPath, mappingPath string
	var fromValues, toValues previewCustomerMigrationDeployments
	if refreshContracts {
		fs.StringVar(&mappingPath, "mapping", "", "version 1 explicit mapping reviewed with the route owner")
		fs.Var(&fromValues, "from-deployment", "baseline deployment as APP=ID; repeat for each app")
		fs.Var(&toValues, "to-deployment", "successor deployment as APP=ID; repeat for each app")
	} else {
		fs.StringVar(&contractPath, "contract-review", "", "version 1 JSON from gregale routes migration review")
	}
	var snapshots previewCustomerCutoverReviewSnapshotPaths
	fs.Var(&snapshots, "snapshot", "saved version 1 customer migration tracker JSON; repeat for each observation window")
	gracePeriodFlag := fs.String("grace-period", "30d", "minimum continuous zero-traffic period required for owner review")
	minWindows := fs.Int("min-windows", 2, "minimum distinct complete observation windows")
	maxStalenessFlag := fs.String("max-staleness", "72h", "maximum age of the latest telemetry watermark")
	format := fs.String("format", "text", "report format: text, markdown, or csv (or use --json)")
	output := fs.String("out", "", "write the machine-readable cutover review to a new JSON file")
	failBreaking := fs.Bool("fail-on-breaking", false, "exit nonzero when a mapped successor has a declared breaking change")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit nonzero when contract or telemetry evidence is incomplete")
	failNotReady := fs.Bool("fail-on-not-ready", false, "exit nonzero unless every route is ready for owner review")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	gracePeriod, graceErr := parsePreviewCustomerMigrationProgressDuration(*gracePeriodFlag)
	maxStaleness, stalenessErr := parsePreviewCustomerMigrationProgressDuration(*maxStalenessFlag)
	missingContracts := contractPath == ""
	if refreshContracts {
		missingContracts = mappingPath == "" || len(fromValues) == 0
	}
	if len(positional) != 0 || missingContracts || len(snapshots) < 2 || len(snapshots) > previewCustomerMigrationProgressMaxSnapshots ||
		graceErr != nil || stalenessErr != nil || *minWindows < 2 || *minWindows > previewCustomerMigrationProgressMaxSnapshots || *minWindows > len(snapshots) ||
		!slices.Contains([]string{"text", "markdown", "csv"}, *format) || (jsonOutput && *format != "text") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale "+command+" "+contractUsage+" --snapshot <TRACKER.json> --snapshot <TRACKER.json> [--grace-period 30d] [--min-windows 2] [--max-staleness 72h] [--format text|markdown|csv] [--out <PATH>] [--fail-on-breaking] [--fail-on-incomplete] [--fail-on-not-ready] [--json]", docsTopic)
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
		tracker, size, err := readPreviewCustomerMigrationProgressSnapshot(path)
		if err != nil {
			return printErr("Could not read migration snapshot", fmt.Errorf("snapshot %d: %w", i+1, err))
		}
		totalSnapshotBytes += size
		if totalSnapshotBytes > api.RouteImpactReportMaxBytes {
			return printErr("Could not read migration snapshot", errors.New("combined snapshots exceed the 64 MiB limit"))
		}
		data, err := indexPreviewCustomerMigrationProgressSnapshot(tracker)
		if err != nil {
			return printErr("Invalid migration snapshot", fmt.Errorf("snapshot %d: %w", i+1, err))
		}
		loaded = append(loaded, data)
	}
	var err error
	var contracts routeMigrationReviewReport
	if refreshContracts {
		contracts, err = readRouteMigrationReadinessContracts(mappingPath, fromValues, toValues)
	} else {
		contracts, _, err = readPreviewCustomerCutoverContractReview(contractPath)
	}
	if err != nil {
		return printErr("Could not read route contract review", err)
	}
	now := time.Now().UTC()
	progress, err := buildPreviewCustomerMigrationProgressReport(loaded, gracePeriod, *gracePeriodFlag, *minWindows, maxStaleness, *maxStalenessFlag, now)
	if err != nil {
		return printErr("Could not compare migration snapshots", err)
	}
	report, err := buildPreviewCustomerCutoverReview(contracts, progress, loaded, now)
	if err != nil {
		return printErr("Could not join contract and customer evidence", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode cutover review", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save cutover review", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		var rendered bytes.Buffer
		switch *format {
		case "csv":
			if err := renderPreviewCustomerCutoverReviewCSV(&rendered, report); err != nil {
				return printErr("Could not encode cutover action queue", err)
			}
		case "markdown":
			renderPreviewCustomerCutoverReviewMarkdown(&rendered, report)
		default:
			renderPreviewCustomerCutoverReviewText(&rendered, report)
		}
		if _, err := osStdout.Write(rendered.Bytes()); err != nil {
			return printErr("Could not write cutover review", err)
		}
	}
	if *failBreaking && report.Summary.BreakingContractRoutes > 0 {
		return 1
	}
	if *failIncomplete && previewCustomerCutoverHasIncompleteEvidence(report) {
		return 1
	}
	if *failNotReady && report.Outcome != "owner_review_ready" {
		return 1
	}
	return 0
}

// Incompleteness is independent of the headline outcome: a breaking contract
// must not hide missing telemetry or another successor's unknown comparison.
func previewCustomerCutoverHasIncompleteEvidence(report previewCustomerCutoverReviewReport) bool {
	if report.Summary.IncompleteCustomerLinks > 0 {
		return true
	}
	for _, route := range report.Routes {
		if route.ContractStatus == "unknown" || route.TelemetryStatus == "incomplete" {
			return true
		}
		for _, successor := range route.Successors {
			if successor.Status == "unknown" {
				return true
			}
		}
	}
	return false
}

func readRouteMigrationReadinessContracts(mappingPath string, fromValues, toValues previewCustomerMigrationDeployments) (routeMigrationReviewReport, error) {
	mappings, err := readPreviewCustomerMigrationMappings(mappingPath)
	if err != nil {
		return routeMigrationReviewReport{}, err
	}
	fromIDs, err := parsePreviewCustomerMigrationDeployments(fromValues)
	if err != nil {
		return routeMigrationReviewReport{}, fmt.Errorf("invalid --from-deployment: %w", err)
	}
	toIDs, err := parsePreviewCustomerMigrationDeployments(toValues)
	if err != nil {
		return routeMigrationReviewReport{}, fmt.Errorf("invalid --to-deployment: %w", err)
	}
	fromApps, toApps, pairs := routeMigrationRequiredApps(mappings)
	if len(mappings) == 0 || len(mappings) > previewCustomerMigrationMaxLinks || pairs > previewCustomerMigrationMaxLinks ||
		len(fromApps) > previewCustomerMigrationMaxApps || len(toApps) > previewCustomerMigrationMaxApps {
		return routeMigrationReviewReport{}, errors.New("mapping must be nonempty and within the route migration app and link limits")
	}
	if err := validateRouteMigrationDeploymentSet("from", fromApps, fromIDs); err != nil {
		return routeMigrationReviewReport{}, err
	}
	if err := validateRouteMigrationDeploymentSet("to", toApps, toIDs); err != nil {
		return routeMigrationReviewReport{}, err
	}
	client, err := authedClient()
	if err != nil {
		return routeMigrationReviewReport{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	from, err := readRouteMigrationDeploymentSet(ctx, client, fromIDs)
	if err != nil {
		return routeMigrationReviewReport{}, err
	}
	to, err := readRouteMigrationDeploymentSet(ctx, client, toIDs)
	if err != nil {
		return routeMigrationReviewReport{}, err
	}
	return buildRouteMigrationReviewReport(mappings, from, to, time.Now().UTC())
}

type previewCustomerCutoverAction struct {
	priority       string
	priorityRank   int
	priorityReason string
	customer       previewCustomerCutoverReviewCustomer
	route          previewCustomerMigrationEndpoint
	lastObservedAt time.Time
}

func previewCustomerCutoverActionPriority(customer previewCustomerCutoverReviewCustomer) (string, int, string) {
	for _, successor := range customer.LatestSuccessors {
		if successor.ContractStatus == "breaking" {
			return "P0", 0, "customer is using a successor with a breaking contract"
		}
	}
	if customer.NextStep == "move_customer_from_breaking_successor" {
		return "P0", 0, "customer is using a successor with a breaking contract"
	}
	if customer.ObservedOldRouteReuse != nil {
		return "P1", 1, "old route was observed after successor traffic; validate the route change with this customer"
	}
	switch customer.NextStep {
	case "pause_migration_and_review_contract_change":
		return "P1", 1, "mapped successor has a breaking contract; review before migrating customers"
	case "resolve_observed_successor_contract_evidence":
		return "P1", 1, "customer is using a successor whose contract evidence is unknown"
	case "review_observed_successor_with_route_owner", "review_contract_with_route_owner":
		return "P1", 1, "successor contract needs route-owner review"
	case "resolve_customer_telemetry_gap", "complete_contract_evidence":
		return "P2", 2, "evidence gap prevents a reliable migration decision"
	case "migrate_customer_from_old_route", "complete_cutover_to_observed_successor", "confirm_old_route_shutdown":
		return "P2", 2, "customer still has old-route migration work"
	case "verify_successor_usage_with_customer_or_owner", "verify_usage_with_customer_or_owner":
		if customer.LatestSuccessorEvidence == "ambiguous" || customer.LatestSuccessorEvidence == "none" {
			return "P3", 3, "current successor usage is missing or has tied timestamps; verify with the customer"
		}
		return "P4", 4, "confirm the customer's successor usage before closing migration work"
	default:
		return "P3", 3, "review the customer evidence and confirm the next step"
	}
}

func previewCustomerCutoverActionLastObservedAt(customer previewCustomerCutoverReviewCustomer) time.Time {
	var latest time.Time
	for _, observation := range customer.OldRouteObservations {
		if observed, err := time.Parse(time.RFC3339Nano, observation.LastObservedAt); err == nil && observed.After(latest) {
			latest = observed
		}
	}
	for _, successor := range customer.LatestSuccessors {
		if successor.LastObservedAt.After(latest) {
			latest = successor.LastObservedAt
		}
	}
	if signal := customer.ObservedOldRouteReuse; signal != nil && signal.OldRouteLastObservedAt.After(latest) {
		latest = signal.OldRouteLastObservedAt
	}
	return latest
}

func previewCustomerCutoverActions(report previewCustomerCutoverReviewReport) []previewCustomerCutoverAction {
	var actions []previewCustomerCutoverAction
	for _, route := range report.Routes {
		for _, customer := range route.Customers {
			priority, rank, reason := previewCustomerCutoverActionPriority(customer)
			actions = append(actions, previewCustomerCutoverAction{
				priority: priority, priorityRank: rank, priorityReason: reason,
				customer: customer, route: route.From,
				lastObservedAt: previewCustomerCutoverActionLastObservedAt(customer),
			})
		}
	}
	sort.Slice(actions, func(i, j int) bool {
		left, right := actions[i], actions[j]
		if left.priorityRank != right.priorityRank {
			return left.priorityRank < right.priorityRank
		}
		if !left.lastObservedAt.Equal(right.lastObservedAt) {
			return left.lastObservedAt.After(right.lastObservedAt)
		}
		if left.customer.ID != right.customer.ID {
			return left.customer.ID < right.customer.ID
		}
		if left.customer.App != right.customer.App {
			return left.customer.App < right.customer.App
		}
		return migrationEndpointLess(left.route, right.route)
	})
	return actions
}

func previewCustomerCutoverActionLatestSuccessors(customer previewCustomerCutoverReviewCustomer) string {
	values := make([]string, 0, len(customer.LatestSuccessors))
	for _, successor := range customer.LatestSuccessors {
		values = append(values, fmt.Sprintf("%s %s %s (%s; %s; %d requests)",
			successor.Route.App, successor.Route.Method, successor.Route.Path,
			successor.ContractStatus, successor.LastObservedAt.Format(time.RFC3339Nano), successor.Requests))
	}
	return strings.Join(values, "; ")
}

func renderPreviewCustomerCutoverReviewCSV(w io.Writer, report previewCustomerCutoverReviewReport) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{
		"priority", "priority_reason", "customer_id", "identity_scope", "customer_app",
		"old_route_app", "old_route_method", "old_route_path", "migration_evidence",
		"old_route_observed_windows", "successor_observed_windows", "latest_successor_evidence",
		"latest_successors", "observed_old_route_reuse", "old_route_reused_at", "next_step", "latest_event_at",
	}); err != nil {
		return err
	}
	for _, action := range previewCustomerCutoverActions(report) {
		customer := action.customer
		reuseSignal, oldRouteReusedAt := "", ""
		if signal := customer.ObservedOldRouteReuse; signal != nil {
			reuseSignal = signal.Signal
			oldRouteReusedAt = signal.OldRouteLastObservedAt.Format(time.RFC3339Nano)
		}
		latestEventAt := ""
		if !action.lastObservedAt.IsZero() {
			latestEventAt = action.lastObservedAt.Format(time.RFC3339Nano)
		}
		row := []string{
			action.priority, action.priorityReason, customer.ID, customer.IdentityScope, customer.App,
			action.route.App, action.route.Method, action.route.Path, customer.MigrationEvidence,
			fmt.Sprint(customer.OldRouteObservedWindows), fmt.Sprint(customer.SuccessorWindows), customer.LatestSuccessorEvidence,
			previewCustomerCutoverActionLatestSuccessors(customer), reuseSignal, oldRouteReusedAt, customer.NextStep, latestEventAt,
		}
		for i := range row {
			row[i] = safePreviewCustomerCSVCell(row[i])
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func readPreviewCustomerCutoverContractReview(path string) (routeMigrationReviewReport, int64, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return routeMigrationReviewReport{}, 0, errors.New("use a readable regular contract review file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil || int64(len(body)) > api.RouteImpactReportMaxBytes {
		return routeMigrationReviewReport{}, 0, errors.New("could not read contract review or it exceeds the 64 MiB limit")
	}
	var report routeMigrationReviewReport
	if err := json.Unmarshal(body, &report); err != nil || report.Version != routeMigrationReviewVersion {
		return routeMigrationReviewReport{}, 0, errors.New("contract review must be version 1 JSON from gregale routes migration review")
	}
	return report, int64(len(body)), nil
}

func buildPreviewCustomerCutoverReview(
	contracts routeMigrationReviewReport,
	progress previewCustomerMigrationProgressReport,
	snapshots []previewCustomerMigrationProgressSnapshotData,
	now time.Time,
) (previewCustomerCutoverReviewReport, error) {
	if err := validatePreviewCustomerCutoverContractReview(contracts); err != nil {
		return previewCustomerCutoverReviewReport{}, err
	}
	if len(snapshots) < 2 {
		return previewCustomerCutoverReviewReport{}, errors.New("at least two indexed telemetry snapshots are required")
	}
	for _, snapshot := range snapshots {
		if err := validatePreviewCustomerCutoverDeploymentBinding(contracts, snapshot); err != nil {
			return previewCustomerCutoverReviewReport{}, err
		}
	}
	if len(progress.Routes) == 0 || len(progress.Routes) != len(contracts.Mappings) {
		return previewCustomerCutoverReviewReport{}, errors.New("every contract mapping must have a tracked source route; narrow the mapping or supply complete customer snapshots")
	}
	contractMappings := make(map[previewCustomerMigrationRouteKey]routeMigrationRouteReview, len(contracts.Mappings))
	for _, mapping := range contracts.Mappings {
		contractMappings[previewCustomerMigrationEndpointKey(mapping.From)] = mapping
	}
	report := previewCustomerCutoverReviewReport{
		Version: previewCustomerCutoverReviewVersion, GeneratedAt: now.UTC(), GroupBy: progress.GroupBy, OwnerReviewRequired: true,
		ContractReviewGeneratedAt: contracts.GeneratedAt.UTC(), ContractReviewOutcome: contracts.Outcome,
		FromDeployments: append([]routeMigrationDeploymentEvidence{}, contracts.FromDeployments...),
		ToDeployments:   append([]routeMigrationDeploymentEvidence{}, contracts.ToDeployments...),
		GracePeriod:     progress.GracePeriod, MinWindows: progress.MinWindows, MaxStaleness: progress.MaxStaleness,
		Snapshots: append([]previewCustomerMigrationProgressSnapshot{}, progress.Snapshots...),
		Routes:    []previewCustomerCutoverReviewRoute{}, Outcome: "owner_review_ready",
		Caveats: []string{
			"owner_review_ready requires both a no_supported_breaks contract mapping and complete aggregate zero-traffic evidence for the full grace period; it is a human review checkpoint and never authorizes automatic route removal.",
			"Customer successor observations show traffic was seen, not that a customer's migration is complete. no_current_evidence means telemetry is silent and must not be treated as proof of adoption or non-use.",
			"Successor request counts are reported per saved snapshot because observation windows may overlap; do not add counts across snapshots.",
			"Old-route observations also retain their individual snapshot windows. Missing or out-of-window last-observed timestamps are omitted, not inferred from snapshot creation time. Counts across overlapping windows must not be added.",
			"Historical successor compatibility categories count endpoints observed in any supplied snapshot and may overlap when a customer-route link used multiple successor endpoints.",
			"Latest successor guidance uses the maximum in-window last_observed_at timestamp across supplied snapshots. If an observation with a potentially later window has a missing or invalid timestamp, latest_successor_evidence is ambiguous and the next step asks for verification. Latest contract-status categories can overlap when successor endpoints tie on last-observed time.",
			"old_route_reobserved_after_successor is emitted only when the latest snapshot has available old-route evidence with positive requests and in-window last-observed timestamps place that activity after the latest observed successor use. Missing or ambiguous timestamps do not produce this signal; it reports activity, not customer intent.",
			"Contract and telemetry reports are local evidence files. Target deployment IDs and route mappings are checked for consistency; the contract review records capture source, time, and SHA-256 for the exact declared API contracts compared.",
		},
	}
	report.Caveats = appendUniqueCutoverCaveats(report.Caveats, contracts.Caveats...)
	report.Caveats = appendUniqueCutoverCaveats(report.Caveats, progress.Caveats...)
	for _, route := range progress.Routes {
		key := previewCustomerMigrationEndpointKey(route.From)
		contract, ok := contractMappings[key]
		if !ok {
			return previewCustomerCutoverReviewReport{}, fmt.Errorf("contract review has no mapping for tracked route %s %s in %s", route.From.Method, route.From.Path, route.From.App)
		}
		if err := comparePreviewCustomerCutoverSuccessors(route.From, route.Successors, contract); err != nil {
			return previewCustomerCutoverReviewReport{}, err
		}
		row := previewCustomerCutoverReviewRoute{
			From: route.From, ContractStatus: contract.Status, TelemetryStatus: route.Status,
			CohortCustomers: route.CohortCustomers, SuccessorObserved: route.SuccessorObserved,
			NoCurrentEvidence: route.NoCurrentEvidence, OldRouteObserved: route.OldRouteObserved,
			IncompleteCustomers: route.IncompleteCustomers, OldRouteTrafficWindows: route.OldRouteTrafficWindows,
			GracePeriodStart: route.GracePeriodStart, GracePeriodEnd: route.GracePeriodEnd,
			Successors: []previewCustomerCutoverReviewSuccessor{}, Customers: []previewCustomerCutoverReviewCustomer{}, Blockers: append([]string{}, route.Blockers...),
		}
		for _, successor := range contract.Successors {
			findings := make([]routeMigrationFindingSummary, 0, len(successor.Findings))
			for _, finding := range successor.Findings {
				findings = append(findings, routeMigrationFindingSummary{Severity: finding.Severity, Code: finding.Code, Location: finding.Location})
			}
			row.Successors = append(row.Successors, previewCustomerCutoverReviewSuccessor{To: successor.To, Status: successor.Status, Findings: findings})
		}
		for _, customer := range route.Customers {
			identity := previewCustomerMigrationIdentity{id: customer.ID}
			if customer.IdentityScope == "app" {
				identity.app = customer.App
			}
			linkKey := previewCustomerMigrationProgressLinkKey{identity: identity, from: previewCustomerMigrationEndpointKey(route.From)}
			observedSuccessors, err := buildPreviewCustomerCutoverObservedSuccessors(linkKey, route, contract, snapshots)
			if err != nil {
				return previewCustomerCutoverReviewReport{}, err
			}
			latestSuccessors, latestEvidence := previewCustomerCutoverLatestObservedSuccessors(observedSuccessors)
			oldRouteReuse := buildPreviewCustomerCutoverOldRouteReuse(linkKey, observedSuccessors, snapshots)
			nextStep := previewCustomerCutoverCustomerNextStep(contract.Status, customer.Status, observedSuccessors, latestSuccessors, latestEvidence)
			if oldRouteReuse != nil {
				nextStep = "review_customer_old_route_reuse_with_owner"
			}
			row.Customers = append(row.Customers, previewCustomerCutoverReviewCustomer{
				ID: customer.ID, IdentityScope: customer.IdentityScope, App: customer.App,
				MigrationEvidence: customer.Status, NextStep: nextStep,
				OldRouteObservedWindows: customer.OldRouteObservedWindows, SuccessorWindows: customer.SuccessorWindows,
				ObservedSuccessors: observedSuccessors, LatestSuccessorEvidence: latestEvidence,
				LatestSuccessors: latestSuccessors, ObservedOldRouteReuse: oldRouteReuse,
				OldRouteObservations: buildPreviewCustomerCutoverOldRouteObservations(linkKey, snapshots),
			})
		}
		row.Status, row.Reason = previewCustomerCutoverRouteStatus(contract.Status, route.Status, route.Reason)
		if contract.Status != "no_supported_breaks" {
			row.Blockers = appendUniqueCutoverCaveats(row.Blockers, "contract_"+contract.Status)
		}
		for _, successor := range row.Successors {
			if successor.Status != "no_supported_breaks" {
				row.Blockers = appendUniqueCutoverCaveats(row.Blockers, "successor_contract_"+successor.Status)
			}
		}
		if route.Status != "owner_review_ready" && route.Reason != "" {
			row.Blockers = appendUniqueCutoverCaveats(row.Blockers, route.Reason)
		}
		report.Routes = append(report.Routes, row)
		report.Summary.Routes++
		report.Summary.CustomerRouteLinks += len(row.Customers)
		if previewCustomerCutoverHasBreakingSuccessor(row.Successors) {
			report.Summary.BreakingContractRoutes++
		}
		switch row.Status {
		case "owner_review_ready":
			report.Summary.OwnerReviewReadyRoutes++
		case "contract_review_required":
			report.Summary.ContractReviewRequiredRoutes++
		case "contract_unknown":
			report.Summary.ContractUnknownRoutes++
		case "old_route_active":
			report.Summary.OldRouteActiveRoutes++
		case "telemetry_incomplete":
			report.Summary.TelemetryIncompleteRoutes++
		}
		for _, customer := range row.Customers {
			breaking, review, unknown, compatible := false, false, false, false
			for _, successor := range customer.ObservedSuccessors {
				switch successor.ContractStatus {
				case "breaking":
					breaking = true
				case "review_required":
					review = true
				case "unknown":
					unknown = true
				case "no_supported_breaks":
					compatible = true
				}
			}
			if breaking {
				report.Summary.CustomersOnBreakingSuccessors++
			}
			if review {
				report.Summary.CustomersOnReviewSuccessors++
			}
			if unknown {
				report.Summary.CustomersOnUnknownSuccessors++
			}
			if compatible {
				report.Summary.CustomersOnCompatibleSuccessors++
			}
			if customer.LatestSuccessorEvidence == "ambiguous" {
				report.Summary.AmbiguousLatestSuccessorLinks++
			}
			latestBreaking, latestReview, latestUnknown, latestCompatible := false, false, false, false
			if customer.LatestSuccessorEvidence == "determined" {
				for _, successor := range customer.LatestSuccessors {
					switch successor.ContractStatus {
					case "breaking":
						latestBreaking = true
					case "review_required":
						latestReview = true
					case "unknown":
						latestUnknown = true
					case "no_supported_breaks":
						latestCompatible = true
					}
				}
			}
			if latestBreaking {
				report.Summary.LatestBreakingSuccessorLinks++
			}
			if latestReview {
				report.Summary.LatestReviewSuccessorLinks++
			}
			if latestUnknown {
				report.Summary.LatestUnknownSuccessorLinks++
			}
			if latestCompatible {
				report.Summary.LatestCompatibleSuccessorLinks++
			}
			if customer.ObservedOldRouteReuse != nil {
				report.Summary.ObservedOldRouteReuseLinks++
			}
			switch customer.MigrationEvidence {
			case "successor_observed":
				report.Summary.SuccessorObservedLinks++
			case "old_route_active", "old_route_observed":
				report.Summary.OldRouteObservedLinks++
			case "no_current_evidence":
				report.Summary.NoCurrentEvidenceLinks++
			case "incomplete", "in_place_unmeasurable":
				report.Summary.IncompleteCustomerLinks++
			}
		}
	}
	switch {
	case report.Summary.BreakingContractRoutes > 0:
		report.Outcome = "breaking_changes"
	case report.Summary.ContractUnknownRoutes > 0 || report.Summary.TelemetryIncompleteRoutes > 0 || report.Summary.IncompleteCustomerLinks > 0:
		report.Outcome = "incomplete"
	case report.Summary.OwnerReviewReadyRoutes != report.Summary.Routes:
		report.Outcome = "review_required"
	}
	return report, nil
}

func buildPreviewCustomerCutoverOldRouteObservations(linkKey previewCustomerMigrationProgressLinkKey, snapshots []previewCustomerMigrationProgressSnapshotData) []previewCustomerCutoverSuccessorObservation {
	observations := []previewCustomerCutoverSuccessorObservation{}
	for _, snapshot := range snapshots {
		link, ok := snapshot.links[linkKey]
		if !ok || link.OldRouteEvidence != "observed" || link.OldRouteRequests <= 0 {
			continue
		}
		deployment, ok := snapshot.deployments[linkKey.from.app]
		if !ok {
			continue
		}
		observation := previewCustomerCutoverSuccessorObservation{SnapshotGeneratedAt: snapshot.generated, WindowFrom: deployment.From, WindowUntil: deployment.Until, Requests: link.OldRouteRequests}
		if observed, _, _, valid := previewCustomerCutoverTimestampInWindow(link.OldRouteLastObservedAt, deployment); valid {
			observation.LastObservedAt = observed.Format(time.RFC3339Nano)
		}
		observations = append(observations, observation)
	}
	sort.Slice(observations, func(i, j int) bool {
		return observations[i].SnapshotGeneratedAt.Before(observations[j].SnapshotGeneratedAt)
	})
	return observations
}

func buildPreviewCustomerCutoverObservedSuccessors(
	linkKey previewCustomerMigrationProgressLinkKey,
	progressRoute previewCustomerMigrationProgressRoute,
	contract routeMigrationRouteReview,
	snapshots []previewCustomerMigrationProgressSnapshotData,
) ([]previewCustomerCutoverObservedSuccessor, error) {
	pairs := make(map[previewCustomerMigrationRouteKey]routeMigrationPairReview, len(contract.Successors))
	for _, pair := range contract.Successors {
		pairs[previewCustomerMigrationEndpointKey(pair.To)] = pair
	}

	var qualifyingFrom, qualifyingUntil time.Time
	if progressRoute.Status == "owner_review_ready" {
		var fromErr, untilErr error
		qualifyingFrom, fromErr = time.Parse(time.RFC3339Nano, progressRoute.GracePeriodStart)
		qualifyingUntil, untilErr = time.Parse(time.RFC3339Nano, progressRoute.GracePeriodEnd)
		if fromErr != nil || untilErr != nil || !qualifyingUntil.After(qualifyingFrom) {
			return nil, errors.New("owner-review-ready route has invalid qualifying telemetry timestamps")
		}
	}

	bySuccessor := make(map[previewCustomerMigrationRouteKey]*previewCustomerCutoverObservedSuccessor)
	for _, snapshot := range snapshots {
		link, ok := snapshot.links[linkKey]
		if !ok {
			return nil, errors.New("customer-route link is missing from a validated migration snapshot")
		}
		if progressRoute.Status == "owner_review_ready" {
			window, reason := previewCustomerMigrationProgressWindowForApps(snapshot, []string{linkKey.from.app})
			if reason != "" || window.from.Before(qualifyingFrom) || window.until.After(qualifyingUntil) {
				continue
			}
		}
		for _, observed := range link.ObservedSuccessors {
			endpoint, err := normalizePreviewCustomerMigrationEndpoint(observed.Route)
			if err != nil {
				return nil, errors.New("migration snapshot contains an invalid observed successor")
			}
			pair, ok := pairs[previewCustomerMigrationEndpointKey(endpoint)]
			if !ok {
				return nil, fmt.Errorf("observed successor %s %s is missing from the contract review", endpoint.Method, endpoint.Path)
			}
			deployment := snapshot.deployments[endpoint.App]
			windowFrom, err := canonicalizeCutoverTimestamp(deployment.From)
			if err != nil {
				return nil, fmt.Errorf("successor app %s has an invalid observation start", endpoint.App)
			}
			windowUntil, err := canonicalizeCutoverTimestamp(deployment.Until)
			if err != nil {
				return nil, fmt.Errorf("successor app %s has an invalid observation end", endpoint.App)
			}
			lastObservedAt, err := canonicalizeCutoverTimestamp(observed.LastObservedAt)
			if err != nil {
				return nil, fmt.Errorf("observed successor %s %s has an invalid last-observed timestamp", endpoint.Method, endpoint.Path)
			}
			key := previewCustomerMigrationEndpointKey(endpoint)
			row := bySuccessor[key]
			if row == nil {
				row = &previewCustomerCutoverObservedSuccessor{
					Route: endpoint, ContractStatus: pair.Status,
					Observations: []previewCustomerCutoverSuccessorObservation{},
				}
				bySuccessor[key] = row
			}
			row.Observations = append(row.Observations, previewCustomerCutoverSuccessorObservation{
				SnapshotGeneratedAt: snapshot.generated,
				WindowFrom:          windowFrom,
				WindowUntil:         windowUntil,
				Requests:            observed.Requests,
				LastObservedAt:      lastObservedAt,
			})
		}
	}

	keys := make([]previewCustomerMigrationRouteKey, 0, len(bySuccessor))
	for key := range bySuccessor {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return migrationEndpointLess(previewCustomerMigrationEndpoint{App: keys[i].app, Method: keys[i].method, Path: keys[i].path},
			previewCustomerMigrationEndpoint{App: keys[j].app, Method: keys[j].method, Path: keys[j].path})
	})
	result := make([]previewCustomerCutoverObservedSuccessor, 0, len(keys))
	for _, key := range keys {
		row := bySuccessor[key]
		sort.Slice(row.Observations, func(i, j int) bool {
			return row.Observations[i].SnapshotGeneratedAt.Before(row.Observations[j].SnapshotGeneratedAt)
		})
		result = append(result, *row)
	}
	return result, nil
}

type previewCustomerCutoverObservedRouteEvent struct {
	endpoint    previewCustomerMigrationEndpoint
	contract    string
	observedAt  time.Time
	snapshotAt  time.Time
	windowFrom  time.Time
	windowUntil time.Time
}

func buildPreviewCustomerCutoverOldRouteReuse(
	linkKey previewCustomerMigrationProgressLinkKey,
	observedSuccessors []previewCustomerCutoverObservedSuccessor,
	snapshots []previewCustomerMigrationProgressSnapshotData,
) *previewCustomerCutoverOldRouteReuse {
	contractStatus := make(map[previewCustomerMigrationRouteKey]string, len(observedSuccessors))
	for _, successor := range observedSuccessors {
		contractStatus[previewCustomerMigrationEndpointKey(successor.Route)] = successor.ContractStatus
	}
	if len(contractStatus) == 0 {
		return nil
	}

	var latestSnapshot *previewCustomerMigrationProgressSnapshotData
	for i := range snapshots {
		snapshot := &snapshots[i]
		if latestSnapshot == nil || snapshot.generated.After(latestSnapshot.generated) {
			latestSnapshot = snapshot
		}
	}
	if latestSnapshot == nil {
		return nil
	}
	latestLink, ok := latestSnapshot.links[linkKey]
	if !ok || latestLink.OldRouteEvidence != "observed" {
		return nil
	}
	oldDeployment, ok := latestSnapshot.deployments[linkKey.from.app]
	if !ok || oldDeployment.Status != "available" {
		return nil
	}
	oldAt, oldFrom, oldUntil, valid := previewCustomerCutoverTimestampInWindow(latestLink.OldRouteLastObservedAt, oldDeployment)
	if !valid {
		return nil
	}
	oldEvent := previewCustomerCutoverObservedRouteEvent{
		endpoint: latestLink.From, observedAt: oldAt, snapshotAt: latestSnapshot.generated,
		windowFrom: oldFrom, windowUntil: oldUntil,
	}

	var latestSuccessor *previewCustomerCutoverObservedRouteEvent
	ambiguousSuccessorTime := false
	for _, snapshot := range snapshots {
		if snapshot.generated.After(latestSnapshot.generated) {
			continue
		}
		link, ok := snapshot.links[linkKey]
		if !ok {
			continue
		}
		for _, observed := range link.ObservedSuccessors {
			endpoint, err := normalizePreviewCustomerMigrationEndpoint(observed.Route)
			if err != nil {
				continue
			}
			status, reviewed := contractStatus[previewCustomerMigrationEndpointKey(endpoint)]
			if !reviewed {
				continue
			}
			deployment, exists := snapshot.deployments[endpoint.App]
			if !exists || deployment.Status != "available" {
				ambiguousSuccessorTime = true
				continue
			}
			if event, from, until, valid := previewCustomerCutoverTimestampInWindow(observed.LastObservedAt, deployment); valid {
				eventEvidence := &previewCustomerCutoverObservedRouteEvent{
					endpoint: endpoint, contract: status, observedAt: event, snapshotAt: snapshot.generated, windowFrom: from, windowUntil: until,
				}
				if latestSuccessor == nil || eventEvidence.observedAt.After(latestSuccessor.observedAt) ||
					(eventEvidence.observedAt.Equal(latestSuccessor.observedAt) && eventEvidence.snapshotAt.After(latestSuccessor.snapshotAt)) {
					latestSuccessor = eventEvidence
				}
			} else {
				ambiguousSuccessorTime = true
			}
		}
	}
	// A positive signal requires an old-route observation in the latest snapshot,
	// exact in-window timestamps for the observed successor evidence, and an
	// actual event order. Snapshot windows can overlap, so their creation order
	// alone does not establish that the customer returned to the old route.
	if ambiguousSuccessorTime || latestSuccessor == nil || !oldEvent.observedAt.After(latestSuccessor.observedAt) {
		return nil
	}
	return &previewCustomerCutoverOldRouteReuse{
		Signal:    "old_route_reobserved_after_successor",
		Successor: latestSuccessor.endpoint, SuccessorContractStatus: latestSuccessor.contract,
		SuccessorLastObservedAt: latestSuccessor.observedAt, SuccessorSnapshotGeneratedAt: latestSuccessor.snapshotAt,
		SuccessorWindowFrom: latestSuccessor.windowFrom, SuccessorWindowUntil: latestSuccessor.windowUntil,
		OldRouteLastObservedAt: oldEvent.observedAt, OldRouteSnapshotGeneratedAt: oldEvent.snapshotAt,
		OldRouteWindowFrom: oldEvent.windowFrom, OldRouteWindowUntil: oldEvent.windowUntil,
	}
}

func previewCustomerCutoverTimestampInWindow(value string, deployment previewCustomerMigrationAppReport) (time.Time, time.Time, time.Time, bool) {
	event, eventErr := time.Parse(time.RFC3339Nano, value)
	from, fromErr := time.Parse(time.RFC3339Nano, deployment.From)
	until, untilErr := time.Parse(time.RFC3339Nano, deployment.Until)
	if eventErr != nil || fromErr != nil || untilErr != nil || !until.After(from) || event.Before(from) || !event.Before(until) {
		return time.Time{}, time.Time{}, time.Time{}, false
	}
	return event.UTC(), from.UTC(), until.UTC(), true
}

func canonicalizeCutoverTimestamp(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

func validatePreviewCustomerCutoverContractReview(report routeMigrationReviewReport) error {
	if report.Version != routeMigrationReviewVersion || report.GeneratedAt.IsZero() || len(report.Mappings) == 0 {
		return errors.New("contract review is missing its version, generation time, or route mappings")
	}
	fromDeployments := indexRouteMigrationReviewDeployments(report.FromDeployments)
	toDeployments := indexRouteMigrationReviewDeployments(report.ToDeployments)
	if fromDeployments == nil || toDeployments == nil {
		return errors.New("contract review contains an invalid or duplicate deployment identity")
	}
	seen := map[previewCustomerMigrationRouteKey]bool{}
	for i, mapping := range report.Mappings {
		from, err := normalizePreviewCustomerMigrationEndpoint(mapping.From)
		if err != nil || seen[previewCustomerMigrationEndpointKey(from)] {
			return fmt.Errorf("contract review mapping %d has an invalid or duplicate source route", i+1)
		}
		seen[previewCustomerMigrationEndpointKey(from)] = true
		if _, ok := fromDeployments[from.App]; !ok {
			return fmt.Errorf("contract review is missing its baseline deployment for app %s", from.App)
		}
		if mapping.Status != "no_supported_breaks" && mapping.Status != "breaking" && mapping.Status != "review_required" &&
			mapping.Status != "unknown" && mapping.Status != "successor_options" {
			return fmt.Errorf("contract review mapping %s %s has an unsupported status", from.Method, from.Path)
		}
		seenSuccessors := map[previewCustomerMigrationRouteKey]bool{}
		for j, pair := range mapping.Successors {
			to, err := normalizePreviewCustomerMigrationEndpoint(pair.To)
			if err != nil || seenSuccessors[previewCustomerMigrationEndpointKey(to)] {
				return fmt.Errorf("contract review mapping %s %s has an invalid or duplicate successor %d", from.Method, from.Path, j+1)
			}
			seenSuccessors[previewCustomerMigrationEndpointKey(to)] = true
			if _, ok := toDeployments[to.App]; !ok {
				return fmt.Errorf("contract review is missing its successor deployment for app %s", to.App)
			}
			if pair.Status != "no_supported_breaks" && pair.Status != "breaking" && pair.Status != "review_required" && pair.Status != "unknown" {
				return fmt.Errorf("contract review successor %s %s has an unsupported status", to.Method, to.Path)
			}
		}
		if mapping.Status != routeMigrationMappingStatus(mapping.Successors) {
			return fmt.Errorf("contract review mapping %s %s has a status inconsistent with its successor results", from.Method, from.Path)
		}
	}
	return nil
}

func indexRouteMigrationReviewDeployments(values []routeMigrationDeploymentEvidence) map[string]routeMigrationDeploymentEvidence {
	result := make(map[string]routeMigrationDeploymentEvidence, len(values))
	for _, deployment := range values {
		if !validCLISlug(deployment.App) || !canonicalRouteHealthID(deployment.DeploymentID) || result[deployment.App].App != "" ||
			(deployment.Status != "available" && deployment.Status != "incomplete" && deployment.Status != "unavailable") {
			return nil
		}
		result[deployment.App] = deployment
	}
	return result
}

func appendUniqueCutoverCaveats(values []string, additions ...string) []string {
	seen := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return values
}

func validatePreviewCustomerCutoverDeploymentBinding(contracts routeMigrationReviewReport, snapshot previewCustomerMigrationProgressSnapshotData) error {
	for _, target := range contracts.ToDeployments {
		telemetry, ok := snapshot.deployments[target.App]
		if !ok {
			return fmt.Errorf("telemetry snapshots are missing the successor app %s", target.App)
		}
		if telemetry.DeploymentID != target.DeploymentID {
			return fmt.Errorf("telemetry app %s uses deployment %s; contract review used successor deployment %s", target.App, telemetry.DeploymentID, target.DeploymentID)
		}
	}
	return nil
}

func comparePreviewCustomerCutoverSuccessors(from previewCustomerMigrationEndpoint, telemetry []previewCustomerMigrationEndpoint, contract routeMigrationRouteReview) error {
	left := append([]previewCustomerMigrationEndpoint{}, telemetry...)
	right := make([]previewCustomerMigrationEndpoint, 0, len(contract.Successors))
	for _, pair := range contract.Successors {
		right = append(right, pair.To)
	}
	sort.Slice(left, func(i, j int) bool { return migrationEndpointLess(left[i], left[j]) })
	sort.Slice(right, func(i, j int) bool { return migrationEndpointLess(right[i], right[j]) })
	if len(left) != len(right) {
		return fmt.Errorf("contract review successor mapping for %s %s differs from customer telemetry", from.Method, from.Path)
	}
	for i := range left {
		if left[i] != right[i] {
			return fmt.Errorf("contract review successor mapping for %s %s differs from customer telemetry", from.Method, from.Path)
		}
	}
	return nil
}

func previewCustomerCutoverRouteStatus(contractStatus, telemetryStatus, reason string) (string, string) {
	switch contractStatus {
	case "breaking":
		return "breaking_contract", "the declared successor contract has a supported breaking change"
	case "unknown":
		return "contract_unknown", "contract evidence is incomplete or unsupported"
	case "review_required", "successor_options":
		return "contract_review_required", "the declared contract comparison needs route-owner review"
	}
	switch telemetryStatus {
	case "owner_review_ready":
		return "owner_review_ready", ""
	case "old_route_active":
		return "old_route_active", reason
	case "incomplete":
		return "telemetry_incomplete", reason
	default:
		return "insufficient_observation", reason
	}
}

func previewCustomerCutoverHasBreakingSuccessor(successors []previewCustomerCutoverReviewSuccessor) bool {
	for _, successor := range successors {
		if successor.Status == "breaking" {
			return true
		}
	}
	return false
}

func previewCustomerCutoverLatestObservedSuccessors(
	observed []previewCustomerCutoverObservedSuccessor,
) ([]previewCustomerCutoverLatestSuccessor, string) {
	if len(observed) == 0 {
		return []previewCustomerCutoverLatestSuccessor{}, "none"
	}
	type observationEvent struct {
		row previewCustomerCutoverLatestSuccessor
		key previewCustomerMigrationRouteKey
	}
	type untimedObservation struct {
		windowUntil time.Time
		validWindow bool
	}
	events := make([]observationEvent, 0)
	untimed := make([]untimedObservation, 0)
	for _, successor := range observed {
		for _, observation := range successor.Observations {
			from, fromErr := time.Parse(time.RFC3339Nano, observation.WindowFrom)
			until, untilErr := time.Parse(time.RFC3339Nano, observation.WindowUntil)
			windowValid := fromErr == nil && untilErr == nil && until.After(from)
			lastObservedAt, eventErr := time.Parse(time.RFC3339Nano, observation.LastObservedAt)
			if !windowValid || eventErr != nil || lastObservedAt.Before(from) || !lastObservedAt.Before(until) {
				untimed = append(untimed, untimedObservation{windowUntil: until, validWindow: windowValid})
				continue
			}
			events = append(events, observationEvent{
				key: previewCustomerMigrationEndpointKey(successor.Route),
				row: previewCustomerCutoverLatestSuccessor{
					Route: successor.Route, ContractStatus: successor.ContractStatus, Requests: observation.Requests,
					LastObservedAt: lastObservedAt.UTC(), SnapshotGeneratedAt: observation.SnapshotGeneratedAt.UTC(),
					WindowFrom: from.UTC(), WindowUntil: until.UTC(),
				},
			})
		}
	}
	if len(events) == 0 {
		return []previewCustomerCutoverLatestSuccessor{}, "ambiguous"
	}
	latestAt := events[0].row.LastObservedAt
	for _, event := range events[1:] {
		if event.row.LastObservedAt.After(latestAt) {
			latestAt = event.row.LastObservedAt
		}
	}
	for _, missing := range untimed {
		if !missing.validWindow || missing.windowUntil.After(latestAt) {
			return []previewCustomerCutoverLatestSuccessor{}, "ambiguous"
		}
	}
	latestByRoute := make(map[previewCustomerMigrationRouteKey]previewCustomerCutoverLatestSuccessor)
	for _, event := range events {
		if !event.row.LastObservedAt.Equal(latestAt) {
			continue
		}
		current, exists := latestByRoute[event.key]
		if !exists || event.row.SnapshotGeneratedAt.After(current.SnapshotGeneratedAt) {
			latestByRoute[event.key] = event.row
		}
	}
	keys := make([]previewCustomerMigrationRouteKey, 0, len(latestByRoute))
	for key := range latestByRoute {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return migrationEndpointLess(
			previewCustomerMigrationEndpoint{App: keys[i].app, Method: keys[i].method, Path: keys[i].path},
			previewCustomerMigrationEndpoint{App: keys[j].app, Method: keys[j].method, Path: keys[j].path},
		)
	})
	result := make([]previewCustomerCutoverLatestSuccessor, 0, len(keys))
	for _, key := range keys {
		result = append(result, latestByRoute[key])
	}
	return result, "determined"
}

func previewCustomerCutoverNextStep(contractStatus, evidence string) string {
	if contractStatus == "breaking" {
		return "pause_migration_and_review_contract_change"
	}
	if contractStatus == "unknown" {
		return "complete_contract_evidence"
	}
	if contractStatus != "no_supported_breaks" {
		return "review_contract_with_route_owner"
	}
	switch evidence {
	case "old_route_active":
		return "migrate_customer_from_old_route"
	case "old_route_observed":
		return "confirm_old_route_shutdown"
	case "successor_observed":
		return "verify_successor_usage_with_customer_or_owner"
	case "no_current_evidence":
		return "verify_usage_with_customer_or_owner"
	default:
		return "resolve_customer_telemetry_gap"
	}
}

func previewCustomerCutoverCustomerNextStep(
	contractStatus, evidence string,
	observed []previewCustomerCutoverObservedSuccessor,
	latest []previewCustomerCutoverLatestSuccessor,
	latestEvidence string,
) string {
	if latestEvidence == "ambiguous" {
		return "verify_successor_usage_with_customer_or_owner"
	}
	if latestEvidence == "none" && len(observed) > 0 {
		return "verify_successor_usage_with_customer_or_owner"
	}
	if latestEvidence == "determined" {
		for _, successor := range latest {
			if successor.ContractStatus == "breaking" {
				return "move_customer_from_breaking_successor"
			}
		}
		for _, successor := range latest {
			if successor.ContractStatus == "unknown" {
				return "resolve_observed_successor_contract_evidence"
			}
		}
		for _, successor := range latest {
			if successor.ContractStatus == "review_required" {
				return "review_observed_successor_with_route_owner"
			}
		}
		if len(latest) > 0 && (evidence == "old_route_active" || evidence == "old_route_observed") {
			return "complete_cutover_to_observed_successor"
		}
		if len(latest) > 0 {
			return "verify_successor_usage_with_customer_or_owner"
		}
	}
	return previewCustomerCutoverNextStep(contractStatus, evidence)
}

func renderPreviewCustomerCutoverReviewText(w io.Writer, report previewCustomerCutoverReviewReport) {
	_, _ = fmt.Fprintf(w, "Customer route cutover review (%s)\nOutcome: %s; owner-review-ready routes: %d/%d; breaking contracts: %d; contract review required: %d; unknown contracts: %d; active old routes: %d; incomplete telemetry: %d; observed old-route reuse signals: %d\n",
		report.GroupBy, report.Outcome, report.Summary.OwnerReviewReadyRoutes, report.Summary.Routes,
		report.Summary.BreakingContractRoutes, report.Summary.ContractReviewRequiredRoutes, report.Summary.ContractUnknownRoutes,
		report.Summary.OldRouteActiveRoutes, report.Summary.TelemetryIncompleteRoutes, report.Summary.ObservedOldRouteReuseLinks)
	_, _ = fmt.Fprintf(w, "Customers observed on successors in any supplied snapshot: compatible %d; breaking %d; review required %d; unknown %d\n", report.Summary.CustomersOnCompatibleSuccessors,
		report.Summary.CustomersOnBreakingSuccessors, report.Summary.CustomersOnReviewSuccessors, report.Summary.CustomersOnUnknownSuccessors)
	_, _ = fmt.Fprintf(w, "Latest observed successor status: compatible %d; breaking %d; review required %d; unknown %d; ordering ambiguous %d\n",
		report.Summary.LatestCompatibleSuccessorLinks, report.Summary.LatestBreakingSuccessorLinks,
		report.Summary.LatestReviewSuccessorLinks, report.Summary.LatestUnknownSuccessorLinks, report.Summary.AmbiguousLatestSuccessorLinks)
	_, _ = fmt.Fprintf(w, "Evidence: contract review %s; grace period %s across at least %d windows; maximum staleness %s\n", report.ContractReviewGeneratedAt.Format(time.RFC3339), report.GracePeriod, report.MinWindows, report.MaxStaleness)
	renderPreviewCustomerCutoverDeploymentEvidence(w, "Baseline contracts", report.FromDeployments, false)
	renderPreviewCustomerCutoverDeploymentEvidence(w, "Successor contracts", report.ToDeployments, false)
	for _, route := range report.Routes {
		_, _ = fmt.Fprintf(w, "\n%s %s %s: %s (contract %s, telemetry %s; zero-traffic windows %d)", route.From.App, route.From.Method, route.From.Path, route.Status, route.ContractStatus, route.TelemetryStatus, route.OldRouteTrafficWindows)
		if route.Reason != "" {
			_, _ = fmt.Fprintf(w, " — %s", route.Reason)
		}
		_, _ = fmt.Fprintln(w)
		for _, blocker := range route.Blockers {
			_, _ = fmt.Fprintf(w, "  blocker: %s\n", previewReportText(blocker))
		}
		for _, customer := range route.Customers {
			label := customer.ID
			if customer.App != "" {
				label += " (app " + customer.App + ")"
			}
			_, _ = fmt.Fprintf(w, "  %s: %s; old-route windows %d; successor windows %d; latest successor evidence %s; next: %s\n",
				label, customer.MigrationEvidence, customer.OldRouteObservedWindows, customer.SuccessorWindows, customer.LatestSuccessorEvidence, customer.NextStep)
			for _, successor := range customer.LatestSuccessors {
				_, _ = fmt.Fprintf(w, "    latest successor %s %s %s: contract %s; last seen %s; snapshot %s; window %s to %s\n",
					successor.Route.App, successor.Route.Method, successor.Route.Path, successor.ContractStatus,
					successor.LastObservedAt.Format(time.RFC3339Nano), successor.SnapshotGeneratedAt.Format(time.RFC3339Nano),
					successor.WindowFrom.Format(time.RFC3339Nano), successor.WindowUntil.Format(time.RFC3339Nano))
			}
			for _, observation := range customer.OldRouteObservations {
				_, _ = fmt.Fprintf(w, "    old route: %d observed requests; last seen %s; snapshot %s; window %s to %s\n", observation.Requests, previewSourceDisplay(observation.LastObservedAt, false), observation.SnapshotGeneratedAt.Format(time.RFC3339Nano), observation.WindowFrom, observation.WindowUntil)
			}
			if signal := customer.ObservedOldRouteReuse; signal != nil {
				_, _ = fmt.Fprintf(w, "    Signal %s: successor %s %s %s (%s) last observed %s, then old route last observed %s; review with the route owner before retiring the old route.\n",
					signal.Signal, signal.Successor.App, signal.Successor.Method, signal.Successor.Path, signal.SuccessorContractStatus,
					signal.SuccessorLastObservedAt.Format(time.RFC3339Nano), signal.OldRouteLastObservedAt.Format(time.RFC3339Nano))
			}
			for _, successor := range customer.ObservedSuccessors {
				_, _ = fmt.Fprintf(w, "    successor %s %s %s: contract %s\n", successor.Route.App, successor.Route.Method, successor.Route.Path, successor.ContractStatus)
				for _, observation := range successor.Observations {
					_, _ = fmt.Fprintf(w, "      snapshot %s; window %s to %s; requests %d", observation.SnapshotGeneratedAt.Format(time.RFC3339), observation.WindowFrom, observation.WindowUntil, observation.Requests)
					if observation.LastObservedAt != "" {
						_, _ = fmt.Fprintf(w, "; last seen %s", observation.LastObservedAt)
					}
					_, _ = fmt.Fprintln(w)
				}
			}
		}
		for _, successor := range route.Successors {
			_, _ = fmt.Fprintf(w, "  -> %s %s %s: %s\n", successor.To.App, successor.To.Method, successor.To.Path, successor.Status)
			for _, finding := range successor.Findings {
				if finding.Location != "" {
					_, _ = fmt.Fprintf(w, "     %s %s at %s\n", finding.Severity, finding.Code, finding.Location)
				} else {
					_, _ = fmt.Fprintf(w, "     %s %s\n", finding.Severity, finding.Code)
				}
			}
		}
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "Note: %s\n", caveat)
	}
}

func previewCustomerCutoverLatestSuccessorsLabel(customer previewCustomerCutoverReviewCustomer, markdown bool) string {
	if customer.LatestSuccessorEvidence != "determined" || len(customer.LatestSuccessors) == 0 {
		return customer.LatestSuccessorEvidence
	}
	values := make([]string, 0, len(customer.LatestSuccessors))
	for _, successor := range customer.LatestSuccessors {
		app := previewSourceDisplay(successor.Route.App, markdown)
		method := previewSourceDisplay(successor.Route.Method, markdown)
		path := previewSourceDisplay(successor.Route.Path, markdown)
		at := successor.LastObservedAt.Format(time.RFC3339Nano)
		values = append(values, fmt.Sprintf("%s %s %s (%s; %s)", app, method, path, successor.ContractStatus, at))
	}
	return strings.Join(values, "; ")
}

func renderPreviewCustomerCutoverReviewMarkdown(w io.Writer, report previewCustomerCutoverReviewReport) {
	_, _ = fmt.Fprintf(w, "## Customer route cutover review\n\nOutcome: **%s**. Owner-review-ready routes: **%d/%d**; breaking contracts: **%d**; contract review required: **%d**; unknown contracts: **%d**; active old routes: **%d**; incomplete telemetry: **%d**; observed old-route reuse signals: **%d**.\n\n",
		report.Outcome, report.Summary.OwnerReviewReadyRoutes, report.Summary.Routes, report.Summary.BreakingContractRoutes,
		report.Summary.ContractReviewRequiredRoutes, report.Summary.ContractUnknownRoutes, report.Summary.OldRouteActiveRoutes,
		report.Summary.TelemetryIncompleteRoutes, report.Summary.ObservedOldRouteReuseLinks)
	_, _ = fmt.Fprintf(w, "Customer grouping: **%s**. Contract review: `%s`; grace period: **%s** across at least **%d** windows; maximum staleness: **%s**.\n\n",
		report.GroupBy, report.ContractReviewGeneratedAt.Format(time.RFC3339), report.GracePeriod, report.MinWindows, report.MaxStaleness)
	_, _ = fmt.Fprintf(w, "Customers observed on successors in any supplied snapshot: compatible **%d**; breaking **%d**; review required **%d**; unknown **%d**. Counts are customer-route links.\n\n",
		report.Summary.CustomersOnCompatibleSuccessors, report.Summary.CustomersOnBreakingSuccessors,
		report.Summary.CustomersOnReviewSuccessors, report.Summary.CustomersOnUnknownSuccessors)
	_, _ = fmt.Fprintf(w, "Latest observed successor status: compatible **%d**; breaking **%d**; review required **%d**; unknown **%d**; ordering ambiguous **%d**. Categories can overlap when endpoint timestamps tie.\n\n",
		report.Summary.LatestCompatibleSuccessorLinks, report.Summary.LatestBreakingSuccessorLinks,
		report.Summary.LatestReviewSuccessorLinks, report.Summary.LatestUnknownSuccessorLinks, report.Summary.AmbiguousLatestSuccessorLinks)
	renderPreviewCustomerCutoverDeploymentEvidence(w, "Baseline contracts", report.FromDeployments, true)
	renderPreviewCustomerCutoverDeploymentEvidence(w, "Successor contracts", report.ToDeployments, true)
	_, _ = fmt.Fprintln(w, "| Old route | Contract | Telemetry | Cutover status | Customers: successor / old route / no evidence / incomplete | Zero-traffic windows |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---:|---:|")
	for _, route := range report.Routes {
		_, _ = fmt.Fprintf(w, "| `%s %s` in `%s` | **%s** | **%s** | **%s** | %d / %d / %d / %d | %d |\n",
			route.From.Method, previewSourceDisplay(route.From.Path, true), route.From.App, route.ContractStatus, route.TelemetryStatus, route.Status,
			route.SuccessorObserved, route.OldRouteObserved, route.NoCurrentEvidence, route.IncompleteCustomers, route.OldRouteTrafficWindows)
	}
	_, _ = fmt.Fprintln(w)
	for _, route := range report.Routes {
		_, _ = fmt.Fprintf(w, "### `%s %s` in `%s` — %s\n\n", route.From.Method, previewSourceDisplay(route.From.Path, true), route.From.App, route.Status)
		if route.Reason != "" {
			_, _ = fmt.Fprintf(w, "%s\n\n", previewSourceDisplay(route.Reason, true))
		}
		for _, blocker := range route.Blockers {
			_, _ = fmt.Fprintf(w, "- Blocker: %s\n", previewSourceDisplay(blocker, true))
		}
		if len(route.Blockers) > 0 {
			_, _ = fmt.Fprintln(w)
		}
		_, _ = fmt.Fprintln(w, "| Customer ID | Scope | Migration evidence | Old-route windows | Successor windows | Latest successor evidence | Latest observed successor(s) | Observed old-route reuse | Next step |")
		_, _ = fmt.Fprintln(w, "|---|---|---|---:|---:|---|---|---|---|")
		for _, customer := range route.Customers {
			reuse := ""
			if customer.ObservedOldRouteReuse != nil {
				reuse = customer.ObservedOldRouteReuse.Signal
			}
			_, _ = fmt.Fprintf(w, "| %s | %s | **%s** | %d | %d | %s | %s | %s | %s |\n", previewSourceDisplay(customer.ID, true), customer.IdentityScope,
				customer.MigrationEvidence, customer.OldRouteObservedWindows, customer.SuccessorWindows, customer.LatestSuccessorEvidence,
				previewCustomerCutoverLatestSuccessorsLabel(customer, true), reuse, customer.NextStep)
		}
		for _, customer := range route.Customers {
			for _, observation := range customer.OldRouteObservations {
				_, _ = fmt.Fprintf(w, "\nCustomer `%s` old route: **%d** observed requests; last seen %s; snapshot `%s`; window `%s` to `%s`.\n", previewSourceDisplay(customer.ID, true), observation.Requests, previewSourceDisplay(observation.LastObservedAt, true), observation.SnapshotGeneratedAt.Format(time.RFC3339Nano), previewReportText(observation.WindowFrom), previewReportText(observation.WindowUntil))
			}
			if signal := customer.ObservedOldRouteReuse; signal != nil {
				_, _ = fmt.Fprintf(w, "\nCustomer `%s` signal `%s`: successor `%s %s %s` (**%s**) last observed at `%s`, then old route last observed at `%s`. Review route reuse with the customer or owner; this signal reports observed sequence, not intent.\n",
					previewSourceDisplay(customer.ID, true), signal.Signal, previewSourceDisplay(signal.Successor.App, true),
					previewSourceDisplay(signal.Successor.Method, true), previewSourceDisplay(signal.Successor.Path, true), signal.SuccessorContractStatus,
					signal.SuccessorLastObservedAt.Format(time.RFC3339Nano), signal.OldRouteLastObservedAt.Format(time.RFC3339Nano))
			}
			for _, successor := range customer.ObservedSuccessors {
				_, _ = fmt.Fprintf(w, "\nCustomer `%s` observed successor `%s %s %s` (**%s**):\n", previewSourceDisplay(customer.ID, true),
					previewSourceDisplay(successor.Route.App, true), previewSourceDisplay(successor.Route.Method, true), previewSourceDisplay(successor.Route.Path, true), successor.ContractStatus)
				for _, observation := range successor.Observations {
					_, _ = fmt.Fprintf(w, "- Snapshot `%s`, window `%s` to `%s`: **%d** requests", observation.SnapshotGeneratedAt.Format(time.RFC3339),
						previewSourceDisplay(observation.WindowFrom, true), previewSourceDisplay(observation.WindowUntil, true), observation.Requests)
					if observation.LastObservedAt != "" {
						_, _ = fmt.Fprintf(w, "; last seen `%s`", previewSourceDisplay(observation.LastObservedAt, true))
					}
					_, _ = fmt.Fprintln(w)
				}
			}
		}
		for _, successor := range route.Successors {
			_, _ = fmt.Fprintf(w, "\nSuccessor `%s %s %s`: **%s**", successor.To.App, successor.To.Method, previewSourceDisplay(successor.To.Path, true), successor.Status)
			for _, finding := range successor.Findings {
				_, _ = fmt.Fprintf(w, "\n- %s `%s`", finding.Severity, finding.Code)
				if finding.Location != "" {
					_, _ = fmt.Fprintf(w, " at `%s`", previewSourceDisplay(finding.Location, true))
				}
			}
		}
		_, _ = fmt.Fprintln(w)
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "> %s\n\n", caveat)
	}
}

func renderPreviewCustomerCutoverDeploymentEvidence(w io.Writer, heading string, deployments []routeMigrationDeploymentEvidence, markdown bool) {
	if len(deployments) == 0 {
		return
	}
	if markdown {
		_, _ = fmt.Fprintf(w, "**%s**\n\n", heading)
	} else {
		_, _ = fmt.Fprintf(w, "%s:\n", heading)
	}
	for _, deployment := range deployments {
		if markdown {
			_, _ = fmt.Fprintf(w, "- `%s` deployment `%s`: **%s**", previewSourceDisplay(deployment.App, true), deployment.DeploymentID, deployment.Status)
		} else {
			_, _ = fmt.Fprintf(w, "  %s deployment %s: %s", deployment.App, deployment.DeploymentID, deployment.Status)
		}
		if deployment.ContractSource != "" {
			_, _ = fmt.Fprintf(w, ", source `%s`", previewSourceDisplay(deployment.ContractSource, markdown))
		}
		if deployment.CapturedAt != "" {
			_, _ = fmt.Fprintf(w, ", captured `%s`", previewSourceDisplay(deployment.CapturedAt, markdown))
		}
		if deployment.ContractSHA != "" {
			_, _ = fmt.Fprintf(w, ", SHA-256 `%s`", deployment.ContractSHA)
		}
		if deployment.Reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", previewSourceDisplay(deployment.Reason, markdown))
		}
		_, _ = fmt.Fprintln(w)
	}
	if markdown {
		_, _ = fmt.Fprintln(w)
	}
}
