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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

const (
	previewCustomerMigrationVersion  = 1
	previewCustomerMigrationMaxLinks = 100000
	previewCustomerMigrationMaxApps  = 20
	previewCustomerMigrationWorkers  = 4
)

type previewCustomerMigrationEndpoint struct {
	App    string `json:"app"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

type previewCustomerMigrationRouteKey struct {
	app    string
	method string
	path   string
}

type previewCustomerMigrationMapping struct {
	From       previewCustomerMigrationEndpoint   `json:"from"`
	Successors []previewCustomerMigrationEndpoint `json:"successors"`
}

type previewCustomerMigrationMappingFile struct {
	Version  int                               `json:"version"`
	Mappings []previewCustomerMigrationMapping `json:"mappings"`
}

type previewCustomerMigrationAppReport struct {
	App             string `json:"app"`
	DeploymentID    string `json:"deployment_id"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	From            string `json:"from,omitempty"`
	Until           string `json:"until,omitempty"`
	AsOf            string `json:"as_of,omitempty"`
	Coverage        string `json:"coverage,omitempty"`
	WindowClamped   bool   `json:"window_clamped,omitempty"`
	RoutesScanned   int    `json:"routes_scanned,omitempty"`
	RouteLimit      int    `json:"route_limit,omitempty"`
	RoutesTruncated bool   `json:"routes_truncated,omitempty"`
}

type previewCustomerMigrationSummary struct {
	CohortCustomers     int `json:"cohort_customers"`
	CustomerRouteLinks  int `json:"customer_route_links"`
	OldRouteActive      int `json:"old_route_active"`
	SuccessorObserved   int `json:"successor_observed"`
	Both                int `json:"both"`
	NoCurrentEvidence   int `json:"no_current_evidence"`
	InPlaceUnmeasurable int `json:"in_place_unmeasurable"`
	Incomplete          int `json:"incomplete"`
}

type previewCustomerMigrationReport struct {
	Version     int                                 `json:"version"`
	GeneratedAt time.Time                           `json:"generated_at"`
	GroupBy     string                              `json:"group_by"`
	Since       string                              `json:"since"`
	Status      string                              `json:"status"`
	Summary     previewCustomerMigrationSummary     `json:"summary"`
	Deployments []previewCustomerMigrationAppReport `json:"deployments"`
	Customers   []previewCustomerMigrationCustomer  `json:"customers"`
	Caveats     []string                            `json:"caveats"`
}

type previewCustomerMigrationCustomer struct {
	ID            string                          `json:"id"`
	IdentityScope string                          `json:"identity_scope"`
	App           string                          `json:"app,omitempty"`
	Routes        []previewCustomerMigrationRoute `json:"routes"`
}

type previewCustomerMigrationRoute struct {
	Preview                string                              `json:"preview,omitempty"`
	From                   previewCustomerMigrationEndpoint    `json:"from"`
	Successors             []previewCustomerMigrationEndpoint  `json:"successors"`
	OldRouteEvidence       string                              `json:"old_route_evidence"`
	OldRouteRequests       int64                               `json:"old_route_observed_requests"`
	OldRouteLastObservedAt string                              `json:"old_route_last_observed_at,omitempty"`
	OldRouteTotalEvidence  string                              `json:"old_route_total_evidence"`
	OldRouteTotalRequests  int64                               `json:"old_route_total_observed_requests"`
	SuccessorEvidence      string                              `json:"successor_evidence"`
	ObservedSuccessors     []previewCustomerMigrationSuccessor `json:"observed_successors"`
	Status                 string                              `json:"status"`
	BaselineChange         string                              `json:"baseline_change,omitempty"`
	BaselineReasons        []string                            `json:"baseline_reasons,omitempty"`
	BaselineRequests       int64                               `json:"baseline_observed_requests"`
	BaselineLastObservedAt string                              `json:"baseline_last_observed_at,omitempty"`
	BaselineIncomplete     bool                                `json:"baseline_incomplete,omitempty"`
	IncompleteReasons      []string                            `json:"incomplete_reasons,omitempty"`
}

type previewCustomerMigrationSuccessor struct {
	Route          previewCustomerMigrationEndpoint `json:"route"`
	Requests       int64                            `json:"observed_requests"`
	LastObservedAt string                           `json:"last_observed_at,omitempty"`
}

type previewCustomerMigrationIdentity struct {
	id  string
	app string
}

type previewCustomerMigrationAppEvidence struct {
	report previewCustomerMigrationAppReport
	usage  *api.RouteCustomerUsageResponse
	rows   map[routeLifecycleRouteKey]*api.RouteCustomerUsage
	groups map[routeLifecycleRouteKey]previewCustomerMigrationGroupCounts
}

type previewCustomerMigrationGroupCounts struct {
	consumers int
	tenants   int
}

type previewCustomerMigrationRouteEvidence struct {
	observed       bool
	known          bool
	requests       int64
	lastObservedAt string
	reason         string
}

type previewCustomerMigrationBuildInput struct {
	roster    previewCustomerRosterReport
	mappings  map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping
	apps      map[string]previewCustomerMigrationAppEvidence
	since     string
	generated time.Time
}

type previewCustomerMigrationDeployments []string

func (values *previewCustomerMigrationDeployments) String() string {
	return strings.Join(*values, ",")
}

func (values *previewCustomerMigrationDeployments) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func cmdPreviewCustomersTrack(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("preview customers track", flag.ContinueOnError)
	rosterPath := fs.String("roster", "", "version 1 customer roster JSON from gregale preview customers")
	mappingPath := fs.String("mapping", "", "version 1 explicit old-to-successor route mapping JSON")
	since := fs.String("since", "14d", "post-release observation window (duration or RFC3339 timestamp)")
	format := fs.String("format", "text", "report format: text, markdown, or csv (or use --json)")
	output := fs.String("out", "", "write the machine-readable tracker to a new JSON file")
	var deployments previewCustomerMigrationDeployments
	fs.Var(&deployments, "deployment", "immutable current deployment as APP=ID; repeat for each app")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || *rosterPath == "" || *mappingPath == "" || len(deployments) == 0 ||
		!validRouteHealthSuggestionSince(*since) || !slices.Contains([]string{"text", "markdown", "csv"}, *format) ||
		(jsonOutput && *format != "text") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale preview customers track --roster <PATH> --mapping <PATH> --deployment APP=ID [--deployment APP=ID...] [--since 14d] [--format text|markdown|csv] [--out <PATH>] [--json]", "preview")
		return 1
	}
	until := time.Now().UTC()
	if sinceAt, err := time.Parse(time.RFC3339Nano, *since); err == nil && sinceAt.After(until) {
		return printErr("Invalid --since", errors.New("an RFC3339 start time must not be in the future"))
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	roster, err := readPreviewCustomerMigrationRoster(*rosterPath)
	if err != nil {
		return printErr("Could not read customer migration roster", err)
	}
	mappings, err := readPreviewCustomerMigrationMappings(*mappingPath)
	if err != nil {
		return printErr("Could not read route successor mapping", err)
	}
	deploymentByApp, err := parsePreviewCustomerMigrationDeployments(deployments)
	if err != nil {
		return printErr("Invalid --deployment", err)
	}
	if _, err := validatePreviewCustomerMigrationCohort(roster, mappings, deploymentByApp); err != nil {
		return printErr("Invalid migration inputs", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	apps := previewCustomerMigrationRequiredApps(roster, mappings)
	deadline := time.Duration((len(apps)+previewCustomerMigrationWorkers-1)/previewCustomerMigrationWorkers) * api.RouteCheckTimeout
	ctx, cancel := context.WithTimeout(context.Background(), deadline+5*time.Second)
	defer cancel()
	evidence := readPreviewCustomerMigrationApps(ctx, client, apps, deploymentByApp, *since, until)
	report, err := buildPreviewCustomerMigrationReport(previewCustomerMigrationBuildInput{
		roster: roster, mappings: mappings, apps: evidence, since: *since, generated: time.Now().UTC(),
	})
	if err != nil {
		return printErr("Could not build customer migration tracker", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode customer migration tracker", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save customer migration tracker", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	var rendered bytes.Buffer
	switch *format {
	case "markdown":
		renderPreviewCustomerMigrationMarkdown(&rendered, report)
	case "csv":
		if err := renderPreviewCustomerMigrationCSV(&rendered, report); err != nil {
			return printErr("Could not render customer migration tracker", err)
		}
	default:
		renderPreviewCustomerMigrationText(&rendered, report)
	}
	if _, err := osStdout.Write(rendered.Bytes()); err != nil {
		return printErr("Could not write customer migration tracker", err)
	}
	return 0
}

func readPreviewCustomerMigrationRoster(path string) (previewCustomerRosterReport, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return previewCustomerRosterReport{}, errors.New("use a readable regular roster file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil || int64(len(body)) > api.RouteImpactReportMaxBytes {
		return previewCustomerRosterReport{}, errors.New("could not read roster or it exceeds the 64 MiB limit")
	}
	var roster previewCustomerRosterReport
	if err := json.Unmarshal(body, &roster); err != nil || roster.Version != previewCustomerRosterVersion {
		return previewCustomerRosterReport{}, errors.New("roster must be a supported version 1 gregale preview customers JSON report")
	}
	return roster, nil
}

func readPreviewCustomerMigrationMappings(path string) (map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, errors.New("use a readable regular mapping file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil || int64(len(body)) > api.RouteImpactReportMaxBytes {
		return nil, errors.New("could not read mapping or it exceeds the 64 MiB limit")
	}
	var document previewCustomerMigrationMappingFile
	if err := json.Unmarshal(body, &document); err != nil || document.Version != 1 || document.Mappings == nil {
		return nil, errors.New("mapping must be version 1 JSON with a mappings array")
	}
	result := make(map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping, len(document.Mappings))
	for i, mapping := range document.Mappings {
		from, err := normalizePreviewCustomerMigrationEndpoint(mapping.From)
		if err != nil {
			return nil, fmt.Errorf("mapping %d has an invalid from route: %w", i+1, err)
		}
		key := previewCustomerMigrationEndpointKey(from)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("mapping %d duplicates source %s %s in app %s", i+1, from.Method, from.Path, from.App)
		}
		mapping.From = from
		seen := map[previewCustomerMigrationRouteKey]bool{}
		for j := range mapping.Successors {
			successor, err := normalizePreviewCustomerMigrationEndpoint(mapping.Successors[j])
			if err != nil {
				return nil, fmt.Errorf("mapping %d successor %d is invalid: %w", i+1, j+1, err)
			}
			successorKey := previewCustomerMigrationEndpointKey(successor)
			if seen[successorKey] {
				return nil, fmt.Errorf("mapping %d repeats successor %s %s in app %s", i+1, successor.Method, successor.Path, successor.App)
			}
			seen[successorKey] = true
			mapping.Successors[j] = successor
		}
		if mapping.Successors == nil {
			mapping.Successors = []previewCustomerMigrationEndpoint{}
		}
		result[key] = mapping
	}
	return result, nil
}

func normalizePreviewCustomerMigrationEndpoint(endpoint previewCustomerMigrationEndpoint) (previewCustomerMigrationEndpoint, error) {
	if !validCLISlug(endpoint.App) {
		return previewCustomerMigrationEndpoint{}, errors.New("app must be a valid app slug")
	}
	endpoint.Method = strings.ToUpper(endpoint.Method)
	zero := int64(0)
	if err := routehealth.Validate(api.SetRouteHealthGateRequest{
		Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: endpoint.Method, Path: endpoint.Path}},
	}); err != nil {
		return previewCustomerMigrationEndpoint{}, errors.New("method or path is not a supported route selector")
	}
	return endpoint, nil
}

func parsePreviewCustomerMigrationDeployments(values []string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for _, value := range values {
		app, id, ok := strings.Cut(value, "=")
		if !ok || !validCLISlug(app) || !canonicalRouteHealthID(id) {
			return nil, errors.New("use APP=canonical-deployment-UUID and repeat --deployment for each app")
		}
		if _, exists := result[app]; exists {
			return nil, fmt.Errorf("deployment for app %s was supplied more than once", app)
		}
		result[app] = id
	}
	return result, nil
}

func validatePreviewCustomerMigrationCohort(
	roster previewCustomerRosterReport,
	mappings map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping,
	deployments map[string]string,
) (int, error) {
	if roster.GroupBy != "consumer" && roster.GroupBy != "tenant" {
		return 0, errors.New("roster group_by must be consumer or tenant")
	}
	if roster.Customers == nil {
		return 0, errors.New("roster customer list is missing")
	}
	seenCustomers := map[previewCustomerMigrationIdentity]bool{}
	seenLinks := map[string]bool{}
	links := 0
	requiredApps := map[string]bool{}
	for i, customer := range roster.Customers {
		id, err := uuid.Parse(customer.ID)
		if err != nil || id == uuid.Nil || id.String() != customer.ID {
			return 0, fmt.Errorf("customer %d has a non-canonical identity", i+1)
		}
		identity := previewCustomerMigrationIdentity{id: customer.ID, app: customer.App}
		if roster.GroupBy == "consumer" {
			if customer.IdentityScope != "app" || !validCLISlug(customer.App) {
				return 0, fmt.Errorf("consumer %s must have app-scoped identity and an app slug", customer.ID)
			}
		} else if customer.IdentityScope != "account" || customer.App != "" {
			return 0, fmt.Errorf("tenant %s must have account-scoped identity without an app scope", customer.ID)
		}
		if seenCustomers[identity] {
			return 0, fmt.Errorf("customer %s appears more than once in the roster", customer.ID)
		}
		seenCustomers[identity] = true
		if len(customer.Routes) == 0 {
			return 0, fmt.Errorf("customer %s has no baseline route links", customer.ID)
		}
		for _, route := range customer.Routes {
			endpoint, err := normalizePreviewCustomerMigrationEndpoint(previewCustomerMigrationEndpoint{App: route.App, Method: route.Method, Path: route.Path})
			if err != nil {
				return 0, fmt.Errorf("customer %s has an invalid baseline route", customer.ID)
			}
			if roster.GroupBy == "consumer" && customer.App != endpoint.App {
				return 0, fmt.Errorf("consumer %s route app does not match its identity scope", customer.ID)
			}
			linkKey := customer.App + "\x00" + customer.ID + "\x00" + endpoint.Method + "\x00" + endpoint.Path
			if seenLinks[linkKey] {
				continue
			}
			seenLinks[linkKey] = true
			links++
			mapping, ok := mappings[previewCustomerMigrationEndpointKey(endpoint)]
			if !ok {
				return 0, fmt.Errorf("no explicit successor mapping for baseline route %s %s in app %s", endpoint.Method, endpoint.Path, endpoint.App)
			}
			requiredApps[endpoint.App] = true
			for _, successor := range mapping.Successors {
				if roster.GroupBy == "consumer" && successor.App != customer.App {
					return 0, fmt.Errorf("consumer identities are app-scoped; use a tenant roster to track successors across apps")
				}
				requiredApps[successor.App] = true
			}
			if links > previewCustomerMigrationMaxLinks {
				return 0, fmt.Errorf("cohort exceeds %d customer-route links", previewCustomerMigrationMaxLinks)
			}
		}
	}
	if links == 0 {
		return 0, errors.New("roster has no customer-route links to track")
	}
	if len(requiredApps) > previewCustomerMigrationMaxApps {
		return 0, fmt.Errorf("cohort uses %d apps; the tracker supports at most %d per run", len(requiredApps), previewCustomerMigrationMaxApps)
	}
	for app := range requiredApps {
		if deployments[app] == "" {
			return 0, fmt.Errorf("supply --deployment %s=<canonical deployment UUID>", app)
		}
	}
	for app := range deployments {
		if !requiredApps[app] {
			return 0, fmt.Errorf("deployment for app %s is not used by a roster route mapping", app)
		}
	}
	return links, nil
}

func previewCustomerMigrationEndpointKey(endpoint previewCustomerMigrationEndpoint) previewCustomerMigrationRouteKey {
	return previewCustomerMigrationRouteKey{app: endpoint.App, method: endpoint.Method, path: endpoint.Path}
}

func previewCustomerMigrationRequiredApps(roster previewCustomerRosterReport, mappings map[previewCustomerMigrationRouteKey]previewCustomerMigrationMapping) []string {
	apps := map[string]bool{}
	for _, customer := range roster.Customers {
		for _, route := range customer.Routes {
			endpoint := previewCustomerMigrationEndpoint{App: route.App, Method: strings.ToUpper(route.Method), Path: route.Path}
			apps[endpoint.App] = true
			for _, successor := range mappings[previewCustomerMigrationEndpointKey(endpoint)].Successors {
				apps[successor.App] = true
			}
		}
	}
	result := make([]string, 0, len(apps))
	for app := range apps {
		result = append(result, app)
	}
	sort.Strings(result)
	return result
}

func readPreviewCustomerMigrationApps(
	ctx context.Context,
	client *api.Client,
	apps []string,
	deployments map[string]string,
	since string,
	until time.Time,
) map[string]previewCustomerMigrationAppEvidence {
	result := make(map[string]previewCustomerMigrationAppEvidence, len(apps))
	var mu sync.Mutex
	jobs := make(chan string)
	workers := min(previewCustomerMigrationWorkers, len(apps))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for app := range jobs {
				evidence := readPreviewCustomerMigrationApp(ctx, client, app, deployments[app], since, until)
				mu.Lock()
				result[app] = evidence
				mu.Unlock()
			}
		}()
	}
	for _, app := range apps {
		jobs <- app
	}
	close(jobs)
	wg.Wait()
	return result
}

func readPreviewCustomerMigrationApp(
	ctx context.Context,
	client *api.Client,
	app, deployment, since string,
	until time.Time,
) previewCustomerMigrationAppEvidence {
	report := previewCustomerMigrationAppReport{App: app, DeploymentID: deployment, Status: "unavailable", Reason: "route_usage_unavailable"}
	usage, err := client.GetAppRouteCustomerUsage(ctx, app, api.RouteCustomerUsageOptions{
		DeploymentID: deployment, Since: since, Until: until.Format(time.RFC3339Nano),
	})
	if err != nil {
		report.Reason = previewReportReadReason(err)
		return previewCustomerMigrationAppEvidence{report: report}
	}
	if _, err := buildRouteHealthSuggestionReport(usage, app, deployment, "tenant", 1); err != nil {
		report.Reason = "route_usage_invalid"
		return previewCustomerMigrationAppEvidence{report: report}
	}
	report = previewCustomerMigrationAppReport{
		App: app, DeploymentID: deployment, Status: "available", From: usage.From, Until: usage.Until, AsOf: usage.AsOf,
		Coverage: usage.Coverage, WindowClamped: usage.WindowClamped, RoutesScanned: len(usage.Routes),
		RouteLimit: usage.RoutesLimit, RoutesTruncated: usage.RoutesTruncated,
	}
	if usage.WindowClamped || usage.RoutesTruncated {
		report.Status = "incomplete"
		if usage.WindowClamped {
			report.Reason = "observation_window_clamped"
		} else {
			report.Reason = "route_inventory_truncated"
		}
	}
	return indexPreviewCustomerMigrationUsage(report, usage)
}

func indexPreviewCustomerMigrationUsage(report previewCustomerMigrationAppReport, usage api.RouteCustomerUsageResponse) previewCustomerMigrationAppEvidence {
	rows := make(map[routeLifecycleRouteKey]*api.RouteCustomerUsage, len(usage.Routes))
	groups := make(map[routeLifecycleRouteKey]previewCustomerMigrationGroupCounts, len(usage.Routes))
	for i := range usage.Routes {
		row := &usage.Routes[i]
		key := routeLifecycleRouteKey{method: row.Method, path: strings.TrimPrefix(row.Route, row.Method+" ")}
		rows[key] = row
		uniqueConsumers := map[string]bool{}
		uniqueTenants := map[string]bool{}
		for _, customer := range row.Customers {
			if id, err := uuid.Parse(customer.ConsumerID); err == nil && id != uuid.Nil {
				uniqueConsumers[id.String()] = true
			}
			if id, err := uuid.Parse(customer.PlatformTenantID); err == nil && id != uuid.Nil {
				uniqueTenants[id.String()] = true
			}
		}
		groups[key] = previewCustomerMigrationGroupCounts{consumers: len(uniqueConsumers), tenants: len(uniqueTenants)}
	}
	return previewCustomerMigrationAppEvidence{report: report, usage: &usage, rows: rows, groups: groups}
}

func buildPreviewCustomerMigrationReport(input previewCustomerMigrationBuildInput) (previewCustomerMigrationReport, error) {
	links, err := validatePreviewCustomerMigrationCohort(input.roster, input.mappings, previewCustomerMigrationDeploymentsMap(input.apps))
	if err != nil {
		return previewCustomerMigrationReport{}, err
	}
	report := previewCustomerMigrationReport{
		Version: previewCustomerMigrationVersion, GeneratedAt: input.generated.UTC(), GroupBy: input.roster.GroupBy,
		Since: input.since, Status: "complete", Customers: []previewCustomerMigrationCustomer{},
		Deployments: []previewCustomerMigrationAppReport{}, Caveats: []string{
			"This report measures identity-linked requests observed on the selected immutable deployments in the requested retained window. No current evidence means no matching request was observed; it does not prove that a customer stopped using a route.",
			"Old-route total request counts cover all observed callers on that route, including requests without a resolved customer identity. They are required for sustained deprecation review.",
			"Successor mappings are supplied explicitly. Gregale does not infer that a renamed, split, or otherwise similar route replaces another route.",
			"Customer IDs are opaque. Telemetry may be sampled, expired, anonymous, unresolved, or bounded, and a customer absent from route details may be unobserved rather than migrated.",
			"An in-place contract change uses the same route for old and successor traffic. Route telemetry cannot distinguish old and updated client behavior on that path.",
		},
	}
	appNames := make([]string, 0, len(input.apps))
	for app := range input.apps {
		appNames = append(appNames, app)
	}
	sort.Strings(appNames)
	for _, app := range appNames {
		appEvidence := input.apps[app]
		report.Deployments = append(report.Deployments, appEvidence.report)
		if appEvidence.report.Status != "available" {
			report.Status = "incomplete"
		}
	}
	byIdentity := make(map[previewCustomerMigrationIdentity]*previewCustomerMigrationCustomer)
	linkSet := map[string]bool{}
	for _, customer := range input.roster.Customers {
		identity := previewCustomerMigrationIdentity{id: customer.ID, app: customer.App}
		entry := byIdentity[identity]
		if entry == nil {
			entry = &previewCustomerMigrationCustomer{ID: customer.ID, IdentityScope: customer.IdentityScope, App: customer.App, Routes: []previewCustomerMigrationRoute{}}
			byIdentity[identity] = entry
		}
		for _, baseRoute := range customer.Routes {
			from, _ := normalizePreviewCustomerMigrationEndpoint(previewCustomerMigrationEndpoint{App: baseRoute.App, Method: baseRoute.Method, Path: baseRoute.Path})
			linkKey := customer.App + "\x00" + customer.ID + "\x00" + from.App + "\x00" + from.Method + "\x00" + from.Path
			if linkSet[linkKey] {
				continue
			}
			linkSet[linkKey] = true
			mapping := input.mappings[previewCustomerMigrationEndpointKey(from)]
			row := previewCustomerMigrationRoute{
				Preview: baseRoute.Preview, From: from, Successors: append([]previewCustomerMigrationEndpoint{}, mapping.Successors...),
				ObservedSuccessors: []previewCustomerMigrationSuccessor{}, Status: "incomplete",
				BaselineChange: baseRoute.Change, BaselineReasons: append([]string{}, baseRoute.Reasons...),
				BaselineRequests: baseRoute.ObservedRequests, BaselineLastObservedAt: baseRoute.LastObservedAt,
				BaselineIncomplete: baseRoute.Incomplete, IncompleteReasons: []string{},
			}
			oldEvidence := previewCustomerMigrationObserve(input.apps[from.App], from, customer.ID, input.roster.GroupBy)
			row.OldRouteEvidence = previewCustomerMigrationEvidenceLabel(oldEvidence)
			row.OldRouteRequests = oldEvidence.requests
			row.OldRouteLastObservedAt = oldEvidence.lastObservedAt
			row.OldRouteTotalEvidence, row.OldRouteTotalRequests = previewCustomerMigrationRouteTotal(input.apps[from.App], from)
			sameRouteSuccessor := false
			successorKnownAbsent := true
			successorUnknown := false
			for _, successor := range mapping.Successors {
				if successor == from {
					sameRouteSuccessor = true
				}
				evidence := previewCustomerMigrationObserve(input.apps[successor.App], successor, customer.ID, input.roster.GroupBy)
				if evidence.observed {
					row.ObservedSuccessors = append(row.ObservedSuccessors, previewCustomerMigrationSuccessor{
						Route: successor, Requests: evidence.requests, LastObservedAt: evidence.lastObservedAt,
					})
				} else if !evidence.known {
					successorUnknown = true
					successorKnownAbsent = false
					row.IncompleteReasons = appendUniqueString(row.IncompleteReasons, "successor_"+evidence.reason)
				}
			}
			if len(row.ObservedSuccessors) > 0 {
				row.SuccessorEvidence = "observed"
			} else if successorUnknown {
				row.SuccessorEvidence = "incomplete"
			} else if len(mapping.Successors) == 0 {
				row.SuccessorEvidence = "not_mapped"
			} else {
				row.SuccessorEvidence = "not_observed"
			}
			if !oldEvidence.known {
				row.IncompleteReasons = appendUniqueString(row.IncompleteReasons, "old_route_"+oldEvidence.reason)
			}
			switch {
			case sameRouteSuccessor:
				row.Status = "in_place_unmeasurable"
				row.IncompleteReasons = appendUniqueString(row.IncompleteReasons, "same_route_cannot_distinguish_old_and_updated_clients")
			case !oldEvidence.known || (len(row.ObservedSuccessors) == 0 && successorUnknown):
				row.Status = "incomplete"
			case oldEvidence.observed && len(row.ObservedSuccessors) > 0:
				row.Status = "both"
			case oldEvidence.observed && successorKnownAbsent:
				row.Status = "old_route_active"
			case !oldEvidence.observed && len(row.ObservedSuccessors) > 0:
				row.Status = "successor_observed"
			default:
				row.Status = "no_current_evidence"
			}
			slices.Sort(row.IncompleteReasons)
			row.ObservedSuccessors = dedupePreviewCustomerMigrationSuccessors(row.ObservedSuccessors)
			entry.Routes = append(entry.Routes, row)
		}
	}
	for _, customer := range byIdentity {
		sort.Slice(customer.Routes, func(i, j int) bool {
			a, b := customer.Routes[i], customer.Routes[j]
			if a.From.App != b.From.App {
				return a.From.App < b.From.App
			}
			if a.From.Method != b.From.Method {
				return a.From.Method < b.From.Method
			}
			return a.From.Path < b.From.Path
		})
		report.Customers = append(report.Customers, *customer)
	}
	sort.Slice(report.Customers, func(i, j int) bool {
		a, b := report.Customers[i], report.Customers[j]
		if a.App != b.App {
			return a.App < b.App
		}
		return a.ID < b.ID
	})
	report.Summary.CohortCustomers = len(report.Customers)
	report.Summary.CustomerRouteLinks = links
	for _, customer := range report.Customers {
		for _, route := range customer.Routes {
			switch route.Status {
			case "old_route_active":
				report.Summary.OldRouteActive++
			case "successor_observed":
				report.Summary.SuccessorObserved++
			case "both":
				report.Summary.Both++
			case "no_current_evidence":
				report.Summary.NoCurrentEvidence++
			case "in_place_unmeasurable":
				report.Summary.InPlaceUnmeasurable++
			default:
				report.Summary.Incomplete++
			}
			if route.Status == "incomplete" || route.BaselineIncomplete {
				report.Status = "incomplete"
			}
		}
	}
	return report, nil
}

func previewCustomerMigrationDeploymentsMap(apps map[string]previewCustomerMigrationAppEvidence) map[string]string {
	result := make(map[string]string, len(apps))
	for app, evidence := range apps {
		result[app] = evidence.report.DeploymentID
	}
	return result
}

func previewCustomerMigrationObserve(app previewCustomerMigrationAppEvidence, endpoint previewCustomerMigrationEndpoint, id, groupBy string) previewCustomerMigrationRouteEvidence {
	if app.usage == nil || app.report.Status == "unavailable" {
		return previewCustomerMigrationRouteEvidence{reason: "telemetry_unavailable"}
	}
	key := routeLifecycleRouteKey{method: endpoint.Method, path: endpoint.Path}
	row, exists := app.rows[key]
	if !exists {
		if app.usage.RoutesTruncated {
			return previewCustomerMigrationRouteEvidence{reason: "route_inventory_truncated"}
		}
		if app.usage.WindowClamped {
			return previewCustomerMigrationRouteEvidence{reason: "observation_window_clamped"}
		}
		return previewCustomerMigrationRouteEvidence{known: true}
	}
	var observedRequests int64
	var lastObservedAt string
	for _, observation := range row.Customers {
		observedID := observation.PlatformTenantID
		if groupBy == "consumer" {
			observedID = observation.ConsumerID
		}
		parsed, err := uuid.Parse(observedID)
		if err == nil && parsed.String() == id && observation.Requests > 0 {
			observedRequests += observation.Requests
			lastObservedAt = laterTimestamp(observation.LastObservedAt, lastObservedAt)
		}
	}
	if observedRequests > 0 {
		return previewCustomerMigrationRouteEvidence{observed: true, known: true, requests: observedRequests, lastObservedAt: lastObservedAt}
	}
	expected := row.PlatformTenantCount
	actual := app.groups[key].tenants
	if groupBy == "consumer" {
		expected = row.ConsumerCount
		actual = app.groups[key].consumers
	}
	if row.CustomersTruncated || int64(actual) < expected {
		return previewCustomerMigrationRouteEvidence{reason: "customer_details_truncated"}
	}
	if app.usage.WindowClamped {
		return previewCustomerMigrationRouteEvidence{reason: "observation_window_clamped"}
	}
	return previewCustomerMigrationRouteEvidence{known: true}
}

func previewCustomerMigrationEvidenceLabel(evidence previewCustomerMigrationRouteEvidence) string {
	if evidence.observed {
		return "observed"
	}
	if evidence.known {
		return "not_observed"
	}
	return "incomplete"
}

func previewCustomerMigrationRouteTotal(app previewCustomerMigrationAppEvidence, endpoint previewCustomerMigrationEndpoint) (string, int64) {
	if app.usage == nil {
		return "incomplete", 0
	}
	row, exists := app.rows[routeLifecycleRouteKey{method: endpoint.Method, path: endpoint.Path}]
	if exists && row.Requests > 0 {
		return "observed", row.Requests
	}
	if app.report.Status != "available" {
		return "incomplete", 0
	}
	if !exists {
		if app.usage.RoutesTruncated || app.usage.WindowClamped {
			return "incomplete", 0
		}
		return "not_observed", 0
	}
	return "not_observed", row.Requests
}

func dedupePreviewCustomerMigrationSuccessors(values []previewCustomerMigrationSuccessor) []previewCustomerMigrationSuccessor {
	indexes := map[previewCustomerMigrationRouteKey]int{}
	result := make([]previewCustomerMigrationSuccessor, 0, len(values))
	for _, value := range values {
		key := previewCustomerMigrationEndpointKey(value.Route)
		if index, exists := indexes[key]; exists {
			result[index].Requests += value.Requests
			result[index].LastObservedAt = laterTimestamp(value.LastObservedAt, result[index].LastObservedAt)
		} else {
			indexes[key] = len(result)
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i].Route, result[j].Route
		if a.App != b.App {
			return a.App < b.App
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Path < b.Path
	})
	return result
}

func appendUniqueString(values []string, value string) []string {
	if !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}

func renderPreviewCustomerMigrationText(w io.Writer, report previewCustomerMigrationReport) {
	_, _ = fmt.Fprintf(w, "Route customer migration tracker (%s; %s)\nStatus: %s; customers: %d; customer-route links: %d; old active: %d; successor observed: %d; both: %d; no current evidence: %d; incomplete: %d; in-place unmeasurable: %d\n",
		report.GroupBy, report.GeneratedAt.Format(time.RFC3339), report.Status, report.Summary.CohortCustomers, report.Summary.CustomerRouteLinks,
		report.Summary.OldRouteActive, report.Summary.SuccessorObserved, report.Summary.Both, report.Summary.NoCurrentEvidence,
		report.Summary.Incomplete, report.Summary.InPlaceUnmeasurable)
	for _, app := range report.Deployments {
		_, _ = fmt.Fprintf(w, "Deployment %s (%s): %s", app.App, app.DeploymentID, app.Status)
		if app.Reason != "" {
			_, _ = fmt.Fprintf(w, " (%s)", app.Reason)
		}
		if app.From != "" {
			_, _ = fmt.Fprintf(w, "; observed %s to %s", app.From, app.Until)
		}
		_, _ = fmt.Fprintln(w)
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "Note: %s\n", caveat)
	}
	for _, customer := range report.Customers {
		label := previewSourceDisplay(customer.ID, false)
		if customer.App != "" {
			label += " (app " + previewSourceDisplay(customer.App, false) + ")"
		}
		for _, route := range customer.Routes {
			_, _ = fmt.Fprintf(w, "\n%s — %s %s in %s: %s (old %s; %d customer requests; total route %s/%d requests; last %s; successor %s)", label, route.From.Method,
				previewSourceDisplay(route.From.Path, false), route.From.App, route.Status, route.OldRouteEvidence,
				route.OldRouteRequests, route.OldRouteTotalEvidence, route.OldRouteTotalRequests, previewSourceDisplay(route.OldRouteLastObservedAt, false), route.SuccessorEvidence)
			if len(route.ObservedSuccessors) > 0 {
				_, _ = fmt.Fprint(w, "; observed successor(s):")
				for _, successor := range route.ObservedSuccessors {
					_, _ = fmt.Fprintf(w, " %s %s in %s (%d requests; last %s)", successor.Route.Method,
						previewSourceDisplay(successor.Route.Path, false), successor.Route.App, successor.Requests,
						previewSourceDisplay(successor.LastObservedAt, false))
				}
			}
			if len(route.IncompleteReasons) > 0 {
				_, _ = fmt.Fprintf(w, "; details: %s", strings.Join(route.IncompleteReasons, ", "))
			}
			_, _ = fmt.Fprintln(w)
		}
	}
}

func renderPreviewCustomerMigrationMarkdown(w io.Writer, report previewCustomerMigrationReport) {
	_, _ = fmt.Fprintf(w, "## Route customer migration tracker\n\nGrouped by **%s**. Status: **%s**. Cohort customers: %d; customer-route links: %d; old active: %d; successor observed: %d; both: %d; no current evidence: %d; incomplete: %d; in-place unmeasurable: %d.\n\n",
		report.GroupBy, report.Status, report.Summary.CohortCustomers, report.Summary.CustomerRouteLinks,
		report.Summary.OldRouteActive, report.Summary.SuccessorObserved, report.Summary.Both, report.Summary.NoCurrentEvidence,
		report.Summary.Incomplete, report.Summary.InPlaceUnmeasurable)
	for _, app := range report.Deployments {
		_, _ = fmt.Fprintf(w, "- `%s` deployment `%s`: **%s**; window %s to %s.\n", app.App, app.DeploymentID, app.Status, app.From, app.Until)
	}
	_, _ = fmt.Fprintln(w)
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "> %s\n\n", caveat)
	}
	_, _ = fmt.Fprintln(w, "| Customer ID | App scope | Previous route | Successor(s) observed | Old evidence | Old requests | Old last observed | Successor evidence | Status | Baseline evidence | Old route total evidence | Old route total requests |")
	_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---:|---|---|---|---|---|---:|")
	for _, customer := range report.Customers {
		for _, route := range customer.Routes {
			successors := make([]string, 0, len(route.ObservedSuccessors))
			for _, successor := range route.ObservedSuccessors {
				successors = append(successors, fmt.Sprintf("%s %s %s (%d requests; last %s)", successor.Route.App, successor.Route.Method, successor.Route.Path, successor.Requests, successor.LastObservedAt))
			}
			baseline := route.BaselineChange
			if route.BaselineIncomplete {
				baseline += " (incomplete)"
			}
			_, _ = fmt.Fprintf(w, "| %s | %s | `%s %s` | %s | %s | %d | %s | %s | **%s** | %s | %s | %d |\n",
				previewSourceDisplay(customer.ID, true), previewSourceDisplay(customer.App, true),
				previewSourceDisplay(route.From.Method, true), previewSourceDisplay(route.From.App+" "+route.From.Path, true),
				previewSourceDisplay(strings.Join(successors, "; "), true), route.OldRouteEvidence, route.OldRouteRequests,
				previewSourceDisplay(route.OldRouteLastObservedAt, true), route.SuccessorEvidence,
				route.Status, previewSourceDisplay(baseline, true), route.OldRouteTotalEvidence, route.OldRouteTotalRequests)
		}
	}
}

func renderPreviewCustomerMigrationCSV(w io.Writer, report previewCustomerMigrationReport) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{"customer_id", "identity_scope", "app_scope", "preview", "old_app", "old_method", "old_path", "successors", "observed_successors", "old_route_evidence", "old_route_observed_requests", "old_route_last_observed_at", "successor_evidence", "status", "baseline_change", "baseline_observed_requests", "baseline_last_observed_at", "baseline_incomplete", "incomplete_reasons", "old_route_total_evidence", "old_route_total_observed_requests"}); err != nil {
		return err
	}
	for _, customer := range report.Customers {
		for _, route := range customer.Routes {
			successors := make([]string, 0, len(route.Successors))
			for _, value := range route.Successors {
				successors = append(successors, value.App+" "+value.Method+" "+value.Path)
			}
			observed := make([]string, 0, len(route.ObservedSuccessors))
			for _, value := range route.ObservedSuccessors {
				observed = append(observed, fmt.Sprintf("%s %s %s (%d requests; %s)", value.Route.App, value.Route.Method, value.Route.Path, value.Requests, value.LastObservedAt))
			}
			row := []string{customer.ID, customer.IdentityScope, customer.App, route.Preview, route.From.App, route.From.Method, route.From.Path,
				strings.Join(successors, ";"), strings.Join(observed, ";"), route.OldRouteEvidence, fmt.Sprint(route.OldRouteRequests), route.OldRouteLastObservedAt, route.SuccessorEvidence, route.Status,
				route.BaselineChange, fmt.Sprint(route.BaselineRequests), route.BaselineLastObservedAt, fmt.Sprint(route.BaselineIncomplete), strings.Join(route.IncompleteReasons, ";"),
				route.OldRouteTotalEvidence, fmt.Sprint(route.OldRouteTotalRequests)}
			for i := range row {
				row[i] = safePreviewCustomerCSVCell(row[i])
			}
			if err := writer.Write(row); err != nil {
				return err
			}
		}
	}
	writer.Flush()
	return writer.Error()
}
