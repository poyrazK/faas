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
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

const routeMigrationSuggestionMaxPairs = 10000
const routeMigrationSuggestionMaxOperations = 2000

type routeMigrationSuggestionReport struct {
	Version                 int                                 `json:"version"`
	GeneratedAt             time.Time                           `json:"generated_at"`
	Status                  string                              `json:"status"`
	OwnerReviewRequired     bool                                `json:"owner_review_required"`
	CustomerDetailsIncluded bool                                `json:"customer_details_included"`
	FromDeployments         []routeMigrationDeploymentEvidence  `json:"from_deployments"`
	ToDeployments           []routeMigrationDeploymentEvidence  `json:"to_deployments"`
	FromObservation         []previewCustomerMigrationAppReport `json:"from_observation"`
	ToObservation           []previewCustomerMigrationAppReport `json:"to_observation"`
	Sources                 []routeMigrationSourceSuggestion    `json:"sources"`
	Draft                   previewCustomerMigrationMappingFile `json:"draft_mapping"`
	Caveats                 []string                            `json:"caveats"`
}

type routeMigrationSourceSuggestion struct {
	From                    previewCustomerMigrationEndpoint    `json:"from"`
	Status                  string                              `json:"status"`
	Traffic                 routeMigrationSuggestionTraffic     `json:"traffic"`
	Candidates              []routeMigrationSuccessorSuggestion `json:"candidates"`
	CandidatesTotal         int                                 `json:"candidates_total"`
	CandidatesTruncated     bool                                `json:"candidates_truncated"`
	CustomerDetailsStatus   string                              `json:"customer_details_status,omitempty"`
	ObservedCustomers       []api.RouteCustomerObservation      `json:"observed_customers,omitempty"`
	CustomersTruncated      bool                                `json:"customers_truncated,omitempty"`
	OmittedCustomerRequests int64                               `json:"omitted_customer_requests,omitempty"`
}

type routeMigrationSuccessorSuggestion struct {
	To             previewCustomerMigrationEndpoint `json:"to"`
	Score          int                              `json:"score"`
	Reasons        []string                         `json:"reasons"`
	ContractStatus string                           `json:"contract_status"`
	Findings       []openapidiff.RoutePairFinding   `json:"findings"`
	Traffic        routeMigrationSuggestionTraffic  `json:"traffic"`
}

type routeMigrationSuggestionTraffic struct {
	Status                     string `json:"status"`
	Requests                   int64  `json:"observed_requests"`
	Consumers                  int64  `json:"observed_consumers"`
	Tenants                    int64  `json:"observed_tenants"`
	AnonymousRequests          int64  `json:"anonymous_requests"`
	UnresolvedIdentityRequests int64  `json:"unresolved_identity_requests"`
	LastObservedAt             string `json:"last_observed_at,omitempty"`
}

func cmdRoutesMigrationSuggest(args []string) int {
	flags, positional := splitArgsForFlags(args, "without-traffic", "customer-details")
	fs := newFlagSet("routes migration suggest", flag.ContinueOnError)
	sourcesPath := fs.String("sources", "", "optional version 1 mapping selecting source routes with empty successors")
	output := fs.String("out", "", "save a draft version 1 mapping to a new file for owner review")
	reportOutput := fs.String("report-out", "", "save the explanations and captured evidence to a new JSON file")
	limit := fs.Int("limit", 3, "candidates shown per source (1–10)")
	since := fs.String("since", "14d", "observed usage window (duration or RFC3339 timestamp)")
	withoutTraffic := fs.Bool("without-traffic", false, "compare captured contracts without reading usage")
	customerDetails := fs.Bool("customer-details", false, "include observed source consumer and tenant UUIDs in the report")
	var fromValues, toValues previewCustomerMigrationDeployments
	fs.Var(&fromValues, "from-deployment", "baseline deployment as APP=ID; repeat for each app")
	fs.Var(&toValues, "to-deployment", "successor deployment as APP=ID; repeat for each app")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || len(fromValues) == 0 || len(toValues) == 0 || *limit < 1 || *limit > 10 || *withoutTraffic && *customerDetails || !validRouteHealthSuggestionSince(*since) || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes migration suggest --from-deployment APP=ID --to-deployment APP=ID [--sources PATH] [--since 14d] [--limit 3] [--without-traffic] [--customer-details] [--out draft.json] [--report-out report.json] [--json]", "cli")
		return 1
	}
	if *output != "" && *output == *reportOutput {
		return printErr("Invalid output paths", errors.New("mapping and report must use different new files"))
	}
	for _, path := range []string{*output, *reportOutput} {
		if path == "" {
			continue
		}
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid output path", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	fromIDs, err := parsePreviewCustomerMigrationDeployments(fromValues)
	if err != nil {
		return printErr("Invalid --from-deployment", err)
	}
	toIDs, err := parsePreviewCustomerMigrationDeployments(toValues)
	if err != nil {
		return printErr("Invalid --to-deployment", err)
	}
	if len(fromIDs) > previewCustomerMigrationMaxApps || len(toIDs) > previewCustomerMigrationMaxApps {
		return printErr("Too many apps", fmt.Errorf("each side supports at most %d apps", previewCustomerMigrationMaxApps))
	}
	var sources []previewCustomerMigrationEndpoint
	if *sourcesPath != "" {
		mappings, err := readPreviewCustomerMigrationMappings(*sourcesPath)
		if err != nil {
			return printErr("Could not read sources", err)
		}
		sources = make([]previewCustomerMigrationEndpoint, 0, len(mappings))
		for _, mapping := range mappings {
			if fromIDs[mapping.From.App] == "" || len(mapping.Successors) != 0 {
				return printErr("Invalid sources", errors.New("each source must name a baseline app and have an empty successors array"))
			}
			sources = append(sources, mapping.From)
		}
		if len(sources) == 0 || len(sources) > routeMigrationSuggestionMaxOperations {
			return printErr("Invalid sources", errors.New("select between 1 and 2000 source routes"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	from, err := readRouteMigrationDeploymentSet(ctx, client, fromIDs)
	if err != nil {
		return printErr("Could not read baseline deployments", err)
	}
	to, err := readRouteMigrationDeploymentSet(ctx, client, toIDs)
	if err != nil {
		return printErr("Could not read successor deployments", err)
	}
	generated := time.Now().UTC()
	fromUsage, toUsage := map[string]previewCustomerMigrationAppEvidence{}, map[string]previewCustomerMigrationAppEvidence{}
	if !*withoutTraffic {
		for _, row := range routeMigrationDeploymentRows(from) {
			fromUsage[row.App] = readPreviewCustomerMigrationApp(ctx, client, row.App, row.DeploymentID, *since, generated)
		}
		for _, row := range routeMigrationDeploymentRows(to) {
			toUsage[row.App] = readPreviewCustomerMigrationApp(ctx, client, row.App, row.DeploymentID, *since, generated)
		}
	}
	report, err := buildRouteMigrationSuggestions(from, to, fromUsage, toUsage, sources, *limit, generated)
	if err != nil {
		return printErr("Could not suggest route successors", err)
	}
	if *customerDetails {
		includeRouteMigrationSourceCustomers(&report, fromUsage)
	}
	for _, target := range []struct {
		path  string
		value any
	}{{*output, report.Draft}, {*reportOutput, report}} {
		if target.path == "" {
			continue
		}
		body, err := json.MarshalIndent(target.value, "", "  ")
		if err != nil {
			return printErr("Could not encode suggestions", err)
		}
		if err := writeRoutePolicyPlan(target.path, append(body, '\n')); err != nil {
			return printErr("Could not save suggestions", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	var rendered bytes.Buffer
	renderRouteMigrationSuggestions(&rendered, report)
	if _, err := osStdout.Write(rendered.Bytes()); err != nil {
		return printErr("Could not write suggestions", err)
	}
	return 0
}

func routeMigrationSuggestionOperations(deployments map[string]routeMigrationLoadedDeployment) ([]previewCustomerMigrationEndpoint, error) {
	endpoints := []previewCustomerMigrationEndpoint{}
	for app, deployment := range deployments {
		if deployment.spec == nil {
			continue
		}
		for path, item := range deployment.spec.Paths {
			if item == nil {
				continue
			}
			for method, op := range item.Methods {
				if op == nil {
					continue
				}
				endpoint, err := normalizePreviewCustomerMigrationEndpoint(previewCustomerMigrationEndpoint{App: app, Method: method, Path: path})
				if err != nil {
					return nil, fmt.Errorf("captured operation in app %s cannot be selected: %s %s", app, method, path)
				}
				endpoints = append(endpoints, endpoint)
				if len(endpoints) > routeMigrationSuggestionMaxOperations {
					return nil, errors.New("captured operation count exceeds 2000 per side; select fewer deployments")
				}
			}
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		return routeMigrationSuggestionKey(endpoints[i]) < routeMigrationSuggestionKey(endpoints[j])
	})
	return endpoints, nil
}

func buildRouteMigrationSuggestions(from, to map[string]routeMigrationLoadedDeployment, fromUsage, toUsage map[string]previewCustomerMigrationAppEvidence, sources []previewCustomerMigrationEndpoint, limit int, generated time.Time) (routeMigrationSuggestionReport, error) {
	report := routeMigrationSuggestionReport{Version: 1, GeneratedAt: generated.UTC(), Status: "owner_review_required", OwnerReviewRequired: true, FromDeployments: routeMigrationDeploymentRows(from), ToDeployments: routeMigrationDeploymentRows(to), FromObservation: []previewCustomerMigrationAppReport{}, ToObservation: []previewCustomerMigrationAppReport{}, Sources: []routeMigrationSourceSuggestion{}, Draft: previewCustomerMigrationMappingFile{Version: 1, Mappings: []previewCustomerMigrationMapping{}}, Caveats: []string{
		"Suggestions are heuristics for owner review, not proof of behavioral equivalence or permission to remove a route.",
		"Scores use method, version-normalized path structure, literal segments, operation IDs and declared contract findings. Traffic only breaks ranking ties; it cannot resolve an ambiguous contract match.",
		"The draft selects only a strong, clear highest-ranked candidate with no supported contract breaks (score at least 70 and a lead greater than 10). Ambiguous, weak, incompatible and incomplete sources keep empty successors.",
		"Usage is retained observed-only evidence for each specified deployment and returned window. Missing observations do not prove inactivity. Customer UUIDs are included only with --customer-details; names, keys and request payloads are absent. Use preview customers track after reviewing the mapping for customer cutover evidence.",
		"Capture sources, timestamps and SHA-256 identify the reviewed contract bytes; a deployment's capture can be replaced by its owner.",
	}}
	candidates, err := routeMigrationSuggestionOperations(to)
	if err != nil {
		return report, err
	}
	if sources == nil {
		sources, err = routeMigrationSuggestionOperations(from)
		if err != nil {
			return report, err
		}
		filtered := sources[:0]
		for _, source := range sources {
			if target := to[source.App]; target.spec != nil && routeMigrationSuggestionOperation(target.spec, source) != nil {
				continue
			}
			filtered = append(filtered, source)
		}
		sources = filtered
	}
	if len(sources)*len(candidates) > routeMigrationSuggestionMaxPairs {
		return report, errors.New("source/candidate search exceeds 10000 pairs; use --sources to narrow the selection")
	}
	sources = append([]previewCustomerMigrationEndpoint(nil), sources...)
	sort.Slice(sources, func(i, j int) bool {
		return routeMigrationSuggestionKey(sources[i]) < routeMigrationSuggestionKey(sources[j])
	})
	incomplete := false
	for _, rows := range []map[string]routeMigrationLoadedDeployment{from, to} {
		for _, row := range rows {
			if row.spec == nil {
				incomplete = true
			}
		}
	}
	for _, side := range []struct {
		usage map[string]previewCustomerMigrationAppEvidence
		rows  *[]previewCustomerMigrationAppReport
	}{{fromUsage, &report.FromObservation}, {toUsage, &report.ToObservation}} {
		apps := []string{}
		for app := range side.usage {
			apps = append(apps, app)
		}
		sort.Strings(apps)
		for _, app := range apps {
			*side.rows = append(*side.rows, side.usage[app].report)
			if side.usage[app].report.Status != "available" {
				incomplete = true
			}
		}
	}
	for _, source := range sources {
		row := routeMigrationSourceSuggestion{From: source, Status: "no_candidate", Traffic: routeMigrationTraffic(source, fromUsage), Candidates: []routeMigrationSuccessorSuggestion{}}
		mapping := previewCustomerMigrationMapping{From: source, Successors: []previewCustomerMigrationEndpoint{}}
		base := from[source.App]
		if base.spec == nil || routeMigrationSuggestionOperation(base.spec, source) == nil {
			row.Status = "source_contract_unavailable"
			incomplete = true
		} else {
			for _, target := range candidates {
				score, reasons := routeMigrationSimilarity(base.spec, source, to[target.App].spec, target)
				if score == 0 {
					continue
				}
				comparison, err := openapidiff.CompareRoutePair(base.spec, source.Method, source.Path, to[target.App].spec, target.Method, target.Path)
				if err != nil {
					return report, err
				}
				switch comparison.Status {
				case "no_supported_breaks":
					score += 20
					reasons = append(reasons, "no_supported_contract_breaks")
				case "review_required":
					score += 5
					reasons = append(reasons, "security_review_required")
				case "breaking":
					reasons = append(reasons, "declared_contract_breaks")
				default:
					reasons = append(reasons, "contract_comparison_incomplete")
				}
				row.Candidates = append(row.Candidates, routeMigrationSuccessorSuggestion{To: target, Score: score, Reasons: reasons, ContractStatus: comparison.Status, Findings: comparison.Findings, Traffic: routeMigrationTraffic(target, toUsage)})
			}
			sort.Slice(row.Candidates, func(i, j int) bool {
				a, b := row.Candidates[i], row.Candidates[j]
				if a.Score != b.Score {
					return a.Score > b.Score
				}
				if a.Traffic.Requests != b.Traffic.Requests {
					return a.Traffic.Requests > b.Traffic.Requests
				}
				return routeMigrationSuggestionKey(a.To) < routeMigrationSuggestionKey(b.To)
			})
			row.CandidatesTotal = len(row.Candidates)
			if len(row.Candidates) > 0 {
				best := row.Candidates[0]
				switch {
				case incompleteContractInventory(to):
					row.Status = "candidate_inventory_incomplete"
				case len(row.Candidates) > 1 && best.Score-row.Candidates[1].Score <= 10:
					row.Status = "ambiguous"
				case best.ContractStatus != "no_supported_breaks":
					row.Status = "contract_review_required"
				case best.Score < 70:
					row.Status = "weak_match"
				default:
					row.Status = "suggested"
					mapping.Successors = append(mapping.Successors, best.To)
				}
			} else if incompleteContractInventory(to) {
				row.Status = "candidate_inventory_incomplete"
			}
			if len(row.Candidates) > limit {
				row.CandidatesTruncated = true
				row.Candidates = row.Candidates[:limit]
			}
		}
		report.Sources = append(report.Sources, row)
		report.Draft.Mappings = append(report.Draft.Mappings, mapping)
	}
	if incomplete {
		report.Status = "incomplete"
	}
	return report, nil
}

func incompleteContractInventory(rows map[string]routeMigrationLoadedDeployment) bool {
	for _, row := range rows {
		if row.spec == nil {
			return true
		}
	}
	return false
}

func routeMigrationSuggestionKey(endpoint previewCustomerMigrationEndpoint) string {
	return endpoint.App + "\x00" + endpoint.Method + "\x00" + endpoint.Path
}

func routeMigrationSuggestionOperation(spec *openapidiff.Spec, endpoint previewCustomerMigrationEndpoint) *openapidiff.Operation {
	if spec == nil || spec.Paths[endpoint.Path] == nil {
		return nil
	}
	return spec.Paths[endpoint.Path].Methods[strings.ToLower(endpoint.Method)]
}

func routeMigrationSimilarity(base *openapidiff.Spec, source previewCustomerMigrationEndpoint, candidate *openapidiff.Spec, target previewCustomerMigrationEndpoint) (int, []string) {
	if source.Method != target.Method {
		return 0, nil
	}
	before, after := routeMigrationPathShape(source.Path), routeMigrationPathShape(target.Path)
	score := 0
	reasons := []string{"http_method_matches"}
	literals := map[string]bool{}
	for _, part := range before {
		if part != "{}" && part != "api" {
			literals[part] = true
		}
	}
	if len(literals) > 0 && strings.Join(before, "/") == strings.Join(after, "/") {
		score += 50
		reasons = append(reasons, "version_normalized_path_matches")
	}
	shared := map[string]bool{}
	for _, part := range after {
		if literals[part] {
			shared[part] = true
		}
	}
	if len(shared) > 0 {
		score += min(20, len(shared)*10)
		reasons = append(reasons, "literal_path_segments_overlap")
	}
	a, b := routeMigrationSuggestionOperation(base, source), routeMigrationSuggestionOperation(candidate, target)
	if a != nil && b != nil {
		id, _ := a.Raw["operationId"].(string)
		nextID, _ := b.Raw["operationId"].(string)
		if id != "" && id == nextID {
			score += 60
			reasons = append(reasons, "operation_id_matches")
		}
	}
	// Identical generic schemas and parameter counts alone do not identify a replacement.
	if score == 0 {
		return 0, nil
	}
	if len(before) == len(after) {
		same := true
		for i := range before {
			if (before[i] == "{}") != (after[i] == "{}") {
				same = false
				break
			}
		}
		if same {
			score += 10
			reasons = append(reasons, "path_parameter_positions_match")
		}
	}
	return score, reasons
}

func routeMigrationPathShape(path string) []string {
	parts := []string{}
	for index, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		if (index == 0 || index == 1 && len(parts) == 1 && parts[0] == "api") && len(part) > 1 && part[0] == 'v' {
			version := true
			for _, r := range part[1:] {
				if r < '0' || r > '9' {
					version = false
					break
				}
			}
			if version {
				continue
			}
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			part = "{}"
		}
		parts = append(parts, part)
	}
	return parts
}

func routeMigrationTraffic(endpoint previewCustomerMigrationEndpoint, usage map[string]previewCustomerMigrationAppEvidence) routeMigrationSuggestionTraffic {
	result := routeMigrationSuggestionTraffic{Status: "not_requested"}
	evidence, exists := usage[endpoint.App]
	if !exists {
		return result
	}
	result.Status = "unknown"
	if evidence.usage == nil {
		return result
	}
	row := evidence.rows[routeLifecycleRouteKey{method: endpoint.Method, path: endpoint.Path}]
	if row == nil {
		if evidence.report.Status == "available" {
			result.Status = "not_observed"
		}
		return result
	}
	result.Status = "observed"
	result.Requests, result.Consumers, result.Tenants = row.Requests, row.ConsumerCount, row.PlatformTenantCount
	result.AnonymousRequests, result.UnresolvedIdentityRequests = row.AnonymousRequests, row.UnresolvedIdentityRequests
	result.LastObservedAt = row.LastObservedAt
	return result
}

func renderRouteMigrationSuggestions(w io.Writer, report routeMigrationSuggestionReport) {
	_, _ = fmt.Fprintf(w, "Route successor suggestions: %s; owner review required\n", report.Status)
	for _, source := range report.Sources {
		_, _ = fmt.Fprintf(w, "%s %s %s: %s; baseline traffic %s (%d requests, %d consumers, %d tenants)\n", source.From.App, source.From.Method, source.From.Path, source.Status, source.Traffic.Status, source.Traffic.Requests, source.Traffic.Consumers, source.Traffic.Tenants)
		for i, candidate := range source.Candidates {
			_, _ = fmt.Fprintf(w, "  %d. %s %s %s: score %d; %s; %s; traffic %s (%d requests)\n", i+1, candidate.To.App, candidate.To.Method, candidate.To.Path, candidate.Score, candidate.ContractStatus, strings.Join(candidate.Reasons, ", "), candidate.Traffic.Status, candidate.Traffic.Requests)
			for _, finding := range candidate.Findings {
				_, _ = fmt.Fprintf(w, "    %s: %s at %s\n", finding.Severity, finding.Code, finding.Location)
			}
		}
		if source.CandidatesTruncated {
			_, _ = fmt.Fprintf(w, "  showing %d of %d candidates\n", len(source.Candidates), source.CandidatesTotal)
		}
		if report.CustomerDetailsIncluded {
			_, _ = fmt.Fprintf(w, "  source customer evidence: %s; details truncated: %t; omitted requests: %d\n", source.CustomerDetailsStatus, source.CustomersTruncated, source.OmittedCustomerRequests)
			for _, customer := range source.ObservedCustomers {
				_, _ = fmt.Fprintf(w, "    consumer %s; tenant %s; %d requests; last observed %s\n", customer.ConsumerID, customer.PlatformTenantID, customer.Requests, customer.LastObservedAt)
			}
		}
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "Note: %s\n", caveat)
	}
}

func includeRouteMigrationSourceCustomers(report *routeMigrationSuggestionReport, usage map[string]previewCustomerMigrationAppEvidence) {
	report.CustomerDetailsIncluded = true
	for i := range report.Sources {
		source := &report.Sources[i]
		source.CustomerDetailsStatus = "unknown"
		evidence := usage[source.From.App]
		if evidence.usage == nil {
			continue
		}
		row := evidence.rows[routeLifecycleRouteKey{method: source.From.Method, path: source.From.Path}]
		if row == nil {
			if evidence.report.Status == "available" {
				source.CustomerDetailsStatus = "not_observed"
			}
			continue
		}
		source.CustomerDetailsStatus = "observed"
		source.CustomersTruncated, source.OmittedCustomerRequests = row.CustomersTruncated, row.OtherCustomerRequests
		if row.CustomersTruncated || evidence.report.Status != "available" {
			source.CustomerDetailsStatus = "incomplete"
		}
		from, _ := time.Parse(time.RFC3339Nano, evidence.usage.From)
		until, _ := time.Parse(time.RFC3339Nano, evidence.usage.Until)
		for _, customer := range row.Customers {
			last, err := time.Parse(time.RFC3339Nano, customer.LastObservedAt)
			if customer.ConsumerID == "" && customer.PlatformTenantID == "" ||
				customer.ConsumerID != "" && !canonicalRouteHealthID(customer.ConsumerID) ||
				customer.PlatformTenantID != "" && !canonicalRouteHealthID(customer.PlatformTenantID) ||
				customer.Requests <= 0 || customer.Requests > row.Requests || err != nil || last.Before(from) || !last.Before(until) {
				source.CustomerDetailsStatus = "incomplete"
				report.Status = "incomplete"
				continue
			}
			source.ObservedCustomers = append(source.ObservedCustomers, customer)
		}
		sort.Slice(source.ObservedCustomers, func(i, j int) bool {
			a, b := source.ObservedCustomers[i], source.ObservedCustomers[j]
			if a.Requests != b.Requests {
				return a.Requests > b.Requests
			}
			return a.ConsumerID+"\x00"+a.PlatformTenantID < b.ConsumerID+"\x00"+b.PlatformTenantID
		})
	}
}
