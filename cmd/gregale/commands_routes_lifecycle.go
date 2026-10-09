package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

const routeLifecycleReviewVersion = 1

type routeLifecycleReviewReport struct {
	Version      int                        `json:"version"`
	GeneratedAt  time.Time                  `json:"generated_at"`
	App          string                     `json:"app"`
	DeploymentID string                     `json:"deployment_id"`
	Outcome      string                     `json:"outcome"`
	Observation  routeLifecycleObservation  `json:"observation"`
	Inventory    routeLifecycleInventory    `json:"inventory"`
	Requirements routeLifecycleRequirements `json:"requirements"`
	Source       routeLifecycleSource       `json:"source"`
	Summary      routeLifecycleSummary      `json:"summary"`
	Routes       []routeLifecycleRoute      `json:"routes"`
	Caveats      []string                   `json:"caveats"`
}

type routeLifecycleObservation struct {
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	From            string `json:"from,omitempty"`
	Until           string `json:"until,omitempty"`
	AsOf            string `json:"as_of,omitempty"`
	Coverage        string `json:"coverage,omitempty"`
	RequestedSince  string `json:"requested_since"`
	WindowClamped   bool   `json:"window_clamped"`
	RoutesScanned   int    `json:"routes_scanned"`
	RouteLimit      int    `json:"route_limit"`
	RoutesTruncated bool   `json:"routes_truncated"`
}

type routeLifecycleInventory struct {
	Status                   string `json:"status"`
	Reason                   string `json:"reason,omitempty"`
	Source                   string `json:"source,omitempty"`
	CaptureSource            string `json:"capture_source,omitempty"`
	CaptureSHA256            string `json:"capture_sha256,omitempty"`
	DocumentSHA256           string `json:"document_sha256,omitempty"`
	CapturedAt               string `json:"captured_at,omitempty"`
	ContractRoutes           int    `json:"contract_routes"`
	UnselectableRoutes       int    `json:"unselectable_routes"`
	ObservedRoutesOutsideDoc int    `json:"observed_routes_outside_contract"`
	RoutesOmitted            int    `json:"routes_omitted_by_limit"`
}

type routeLifecycleRequirements struct {
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	RouteRefs int    `json:"route_references"`
}

type routeLifecycleSource struct {
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	CandidateRevision  string `json:"candidate_revision,omitempty"`
	DeploymentRevision string `json:"deployment_revision,omitempty"`
	BoundToDeployment  bool   `json:"bound_to_deployment"`
	RoutesAnalyzed     int    `json:"routes_analyzed"`
	CurrentRoutes      int    `json:"current_source_routes"`
	SHA256             string `json:"sha256,omitempty"`
}

type routeLifecycleSummary struct {
	ContractRoutes                  int `json:"contract_routes"`
	SourceRoutes                    int `json:"source_routes"`
	ObservedRoutes                  int `json:"observed_routes"`
	ReviewCandidates                int `json:"review_candidates"`
	ProtectedRoutes                 int `json:"protected_routes"`
	UnknownRoutes                   int `json:"unknown_routes"`
	ObservedOnlyRoutes              int `json:"observed_only_routes"`
	SourceOutsideContractRoutes     int `json:"source_outside_contract_routes"`
	ContractMissingFromSourceRoutes int `json:"contract_missing_from_source_routes"`
	ObservedOutsideContractRoutes   int `json:"observed_outside_contract_routes"`
}

type routeLifecycleRoute struct {
	Method           string   `json:"method"`
	Path             string   `json:"path"`
	Status           string   `json:"status"`
	Surfaces         []string `json:"surfaces"`
	Reasons          []string `json:"reasons"`
	Requests         int64    `json:"requests,omitempty"`
	Tenants          int64    `json:"tenants,omitempty"`
	Consumers        int64    `json:"consumers,omitempty"`
	LastObservedAt   string   `json:"last_observed_at,omitempty"`
	ObservationMatch string   `json:"observation_match,omitempty"`
	SourceReference  string   `json:"source_reference"`
	SourceMatch      string   `json:"source_match,omitempty"`
	SourcePath       string   `json:"source_path,omitempty"`
	SourceHandler    string   `json:"source_handler,omitempty"`
	SourceFile       string   `json:"source_file,omitempty"`
	SourceLine       int      `json:"source_line,omitempty"`
	RegistrationFile string   `json:"registration_file,omitempty"`
	RegistrationLine int      `json:"registration_line,omitempty"`
	RequirementRefs  []string `json:"requirement_references"`
	Selectable       bool     `json:"telemetry_selector_supported"`
}

type routeLifecycleRouteKey struct {
	method string
	path   string
}

type routeLifecycleContractRoute struct {
	method     string
	path       string
	key        routeLifecycleRouteKey
	shape      routeLifecycleRouteKey
	selectable bool
}

type routeLifecycleRequirementEvidence struct {
	status string
	reason string
	config *api.RouteRequirementsConfig
	rev    int64
	digest string
}

type routeLifecycleSourceEvidence struct {
	status    string
	reason    string
	revision  string
	deploySHA string
	bound     bool
	digest    string
	routes    int
	refs      map[routeLifecycleRouteKey]routeLifecycleSourceRef
	current   map[routeLifecycleRouteKey]routeimpact.Route
}

type routeLifecycleSourceRef struct {
	present bool
	removed bool
}

func cmdRoutesLifecycle(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "prepare-approval":
			return cmdRoutesLifecyclePrepareApproval(args[1:])
		case "approve":
			return cmdRoutesLifecycleApprove(args[1:])
		case "history":
			return cmdRoutesLifecycleHistory(args[1:])
		case "receipt":
			return cmdRoutesLifecycleReceipt(args[1:])
		}
	}
	if len(args) > 0 && args[0] == "declarations" {
		return cmdRoutesLifecycleDeclarations(args[1:])
	}
	if len(args) > 0 && args[0] == "review" {
		return cmdRoutesLifecycleReview(args[1:])
	}
	PrintUsage(osStderr, "usage: gregale routes lifecycle <review|declarations|prepare-approval|approve|receipt|history> <slug> [options]; declarations requires --from-deployment UUID --to-deployment UUID", "cli")
	return 1
}

func cmdRoutesLifecycleReview(args []string) int {
	flags, positional := splitArgsForFlags(args, "fail-on-incomplete")
	fs := newFlagSet("routes lifecycle review", flag.ContinueOnError)
	deploymentID := fs.String("deployment", "", "immutable deployed route contract to review")
	since := fs.String("since", "14d", "route-usage observation window (deployment plan retention may clamp it)")
	sourcePath := fs.String("source-impact", "", "current source route-impact report, bound to this deployment's commit")
	output := fs.String("out", "", "save the full JSON review to a new file")
	failIncomplete := fs.Bool("fail-on-incomplete", false, "exit nonzero when any route remains inconclusive")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || !canonicalRouteHealthID(*deploymentID) ||
		!validRouteHealthSuggestionSince(*since) || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale routes lifecycle review <slug> --deployment ID [--since 14d] [--source-impact PATH] [--out PATH] [--fail-on-incomplete] [--json]", "cli")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	var sourceReport *routeimpact.Report
	var sourceDigest string
	if *sourcePath != "" {
		report, digest, err := readPreviewSourceImpact(*sourcePath)
		if err != nil {
			return printErr("Invalid --source-impact", err)
		}
		if report.App != positional[0] {
			return printErr("Invalid --source-impact", errors.New("source impact app does not match the requested app"))
		}
		sourceReport, sourceDigest = &report, digest
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	deployment, err := client.GetDeployment(ctx, *deploymentID)
	if err != nil {
		return printErr("Could not read deployment", err)
	}
	if deployment.ID != *deploymentID || deployment.AppID == "" {
		return printErr("Invalid deployment response", errors.New("deployment identity is incomplete"))
	}

	inventory, spec := readRouteLifecycleInventory(ctx, client, positional[0], *deploymentID, deployment.AppID)
	observation, varUsage := readRouteLifecycleUsage(ctx, client, positional[0], *deploymentID, *since)
	requirements := readRouteLifecycleRequirements(ctx, client, positional[0], deployment.AppID)
	source := buildRouteLifecycleSourceEvidence(sourceReport, sourceDigest, deployment, positional[0])
	report := buildRouteLifecycleReviewReport(positional[0], *deploymentID, *since, inventory, spec, observation, varUsage, requirements, source)
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode route lifecycle review", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save route lifecycle review", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		renderRouteLifecycleReview(osStdout, report)
	}
	if *failIncomplete && report.Outcome == "incomplete" {
		return 1
	}
	return 0
}

func readRouteLifecycleInventory(ctx context.Context, client *api.Client, slug, deploymentID, appID string) (routeLifecycleInventory, *openapidiff.Spec) {
	evidence := routeLifecycleInventory{Status: "unavailable", Reason: "deployment_contract_unavailable"}
	document, err := client.GetAppsDeploymentOpenAPIDoc(ctx, slug, deploymentID)
	if err != nil {
		evidence.Reason = previewReportReadReason(err)
		return evidence, nil
	}
	if document.DeploymentID != deploymentID || document.AppID != appID || document.Truncated || len(document.Doc) == 0 ||
		!slices.Contains([]string{"cold_boot", "manual_upload"}, document.Source) {
		evidence.Reason = "deployment_contract_identity_or_capture_incomplete"
		return evidence, nil
	}
	body, err := marshalPreviewReportDocument(document.Doc)
	if err != nil {
		evidence.Reason = "deployment_contract_invalid"
		return evidence, nil
	}
	spec, err := openapidiff.LoadBytes(body)
	if err != nil || spec.OpenAPIVersion() == "" {
		evidence.Reason = "deployment_contract_invalid"
		return evidence, nil
	}
	evidence = routeLifecycleInventory{
		Status: "available", Source: "captured_deployment_openapi", CaptureSource: document.Source,
		CaptureSHA256:  document.DocSHA256,
		DocumentSHA256: fmt.Sprintf("%x", openapidiff.SumSHA256(body)), CapturedAt: document.CapturedAt,
	}
	if len(spec.Paths) == 0 {
		evidence.Status, evidence.Reason = "incomplete", "deployment_contract_has_no_paths"
	}
	return evidence, spec
}

func readRouteLifecycleUsage(ctx context.Context, client *api.Client, slug, deploymentID, since string) (routeLifecycleObservation, *api.RouteCustomerUsageResponse) {
	evidence := routeLifecycleObservation{Status: "unavailable", Reason: "route_usage_unavailable", RequestedSince: since}
	usage, err := client.GetAppRouteCustomerUsage(ctx, slug, api.RouteCustomerUsageOptions{DeploymentID: deploymentID, Since: since})
	if err != nil {
		evidence.Reason = previewReportReadReason(err)
		return evidence, nil
	}
	if _, err := buildRouteHealthSuggestionReport(usage, slug, deploymentID, "tenant", 1); err != nil {
		evidence.Reason = "route_usage_invalid"
		return evidence, nil
	}
	evidence = routeLifecycleObservation{
		Status: "available", From: usage.From, Until: usage.Until, AsOf: usage.AsOf, Coverage: usage.Coverage,
		RequestedSince: since, WindowClamped: usage.WindowClamped,
		RoutesScanned: len(usage.Routes), RouteLimit: usage.RoutesLimit, RoutesTruncated: usage.RoutesTruncated,
	}
	return evidence, &usage
}

func readRouteLifecycleRequirements(ctx context.Context, client *api.Client, slug, appID string) routeLifecycleRequirementEvidence {
	saved, err := client.GetSavedRouteRequirements(ctx, slug)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound && strings.Contains(strings.ToLower(apiErr.Problem.Detail), "saved route requirements") {
			return routeLifecycleRequirementEvidence{status: "none"}
		}
		return routeLifecycleRequirementEvidence{status: "unavailable", reason: previewReportReadReason(err)}
	}
	if saved.AppID != appID {
		return routeLifecycleRequirementEvidence{status: "unavailable", reason: "requirements_app_mismatch"}
	}
	normalized, err := normalizeSavedRequirementsResponse(saved)
	if err != nil {
		return routeLifecycleRequirementEvidence{status: "unavailable", reason: "requirements_invalid"}
	}
	config := normalized.Requirements
	return routeLifecycleRequirementEvidence{status: "available", config: &config, rev: normalized.Revision, digest: normalized.SHA256}
}

func buildRouteLifecycleSourceEvidence(report *routeimpact.Report, digest string, deployment api.DeploymentResponse, app string) routeLifecycleSourceEvidence {
	evidence := routeLifecycleSourceEvidence{
		status: "not_provided", deploySHA: deployment.CommitSHA,
		refs: map[routeLifecycleRouteKey]routeLifecycleSourceRef{}, current: map[routeLifecycleRouteKey]routeimpact.Route{},
	}
	if report == nil {
		return evidence
	}
	evidence.revision, evidence.digest, evidence.routes = report.Candidate.Revision, digest, len(report.Routes)
	if report.App != app {
		evidence.status, evidence.reason = "unbound", "source_app_mismatch"
		return evidence
	}
	deploymentSource := previewSourceDeployment(&deployment, deployment.AppID)
	binding := bindPreviewSource(*report, report.Candidate, deploymentSource)
	if binding.Status != "declared_match" {
		evidence.status, evidence.reason = "unbound", binding.Reason
		if evidence.reason == "" {
			evidence.reason = "source_candidate_does_not_match_deployment"
		}
		return evidence
	}
	evidence.bound = true
	if report.Status == "complete" {
		evidence.status = "complete"
	} else {
		evidence.status, evidence.reason = "incomplete", "source_analysis_incomplete"
	}
	for _, route := range report.Routes {
		key := routeLifecycleRouteKey{method: strings.ToUpper(route.Method), path: route.Path}
		ref := evidence.refs[key]
		if route.After != nil {
			current := *route.After
			if current.Method == "" {
				current.Method = route.Method
			}
			if current.Path == "" {
				current.Path = route.Path
			}
			current.Method = strings.ToUpper(current.Method)
			currentKey := routeLifecycleRouteKey{method: current.Method, path: current.Path}
			evidence.current[currentKey] = current
			key = currentKey
			ref.present = true
		} else if route.Before != nil {
			ref.removed = true
		}
		evidence.refs[key] = ref
	}
	return evidence
}

func buildRouteLifecycleReviewReport(
	app, deploymentID, requestedSince string,
	inventory routeLifecycleInventory, spec *openapidiff.Spec,
	observation routeLifecycleObservation, usage *api.RouteCustomerUsageResponse,
	requirements routeLifecycleRequirementEvidence, source routeLifecycleSourceEvidence,
) routeLifecycleReviewReport {
	observation.RequestedSince = requestedSince
	contractRoutes, unselectable := routeLifecycleContractRoutes(spec)
	inventory.ContractRoutes = len(contractRoutes)
	inventory.UnselectableRoutes += unselectable
	if len(contractRoutes) == 0 {
		if inventory.Status == "available" {
			inventory.Status, inventory.Reason = "incomplete", "deployment_contract_has_no_operations"
		}
	}
	if len(contractRoutes) > api.RouteImpactMaxRoutes {
		inventory.RoutesOmitted = len(contractRoutes) - api.RouteImpactMaxRoutes
		contractRoutes = contractRoutes[:api.RouteImpactMaxRoutes]
		inventory.Status, inventory.Reason = "incomplete", "deployment_contract_route_limit_exceeded"
	}
	if unselectable > 0 && inventory.Status == "available" {
		inventory.Status, inventory.Reason = "incomplete", "contract_contains_unselectable_routes"
	}
	contractSurfaceComplete := spec != nil && inventory.RoutesOmitted == 0 &&
		(inventory.Status == "available" || inventory.Status == "incomplete" && inventory.Reason == "contract_contains_unselectable_routes")
	contractCounts, usageByKey, usageByShape := lifecycleUsageIndexes(contractRoutes, usage)
	sourceByShape := lifecycleCurrentSourceShapes(source.current)
	sourceRefByShape := lifecycleSourceShapes(source.refs)
	report := routeLifecycleReviewReport{
		Version: routeLifecycleReviewVersion, GeneratedAt: time.Now().UTC(), App: app, DeploymentID: deploymentID,
		Observation: observation, Inventory: inventory,
		Requirements: routeLifecycleRequirements{Status: requirements.status, Reason: requirements.reason, Revision: requirements.rev, SHA256: requirements.digest},
		Source: routeLifecycleSource{Status: source.status, Reason: source.reason, CandidateRevision: source.revision,
			DeploymentRevision: source.deploySHA, BoundToDeployment: source.bound, RoutesAnalyzed: source.routes,
			CurrentRoutes: len(source.current), SHA256: source.digest},
		Routes: []routeLifecycleRoute{}, Caveats: []string{},
	}
	if requirements.config != nil {
		report.Requirements.RouteRefs = len(requirements.config.Routes) + len(requirements.config.Public) + len(requirements.config.Groups)
	}
	usedUsage := map[routeLifecycleRouteKey]bool{}
	matchedSource := map[routeLifecycleRouteKey]bool{}
	for _, route := range contractRoutes {
		row := routeLifecycleRoute{
			Method: route.method, Path: route.path, Status: "unknown", Surfaces: []string{"captured_contract"}, Reasons: []string{},
			SourceReference: lifecycleSourceReference(route, source, sourceRefByShape),
			RequirementRefs: lifecycleRequirementReferences(route, requirements.config), Selectable: route.selectable,
		}
		sourceRoute, sourceMatch, sourceAmbiguous := lifecycleFindSourceRoute(route, contractCounts, source.current, sourceByShape)
		if sourceRoute != nil {
			row.SourceReference = "present"
			row.Surfaces = append(row.Surfaces, "current_source")
			applyLifecycleSourceRoute(&row, *sourceRoute, sourceMatch)
			matchedSource[routeLifecycleRouteKey{method: strings.ToUpper(sourceRoute.Method), path: sourceRoute.Path}] = true
		} else if sourceAmbiguous {
			row.SourceReference = "unknown"
		}
		usageRow, match, ambiguous := lifecycleFindUsage(route, contractCounts, usageByKey, usageByShape)
		ambiguous = ambiguous || sourceAmbiguous || contractCounts[route.shape] > 1
		if usageRow != nil {
			row.Status, row.ObservationMatch = "active", match
			applyLifecycleUsage(&row, *usageRow)
			row.Surfaces = append(row.Surfaces, "observed_traffic")
			usedUsage[lifecycleUsageKey(*usageRow)] = true
			row.Reasons = lifecycleRouteReasons(row.Status, route.selectable, ambiguous, report.Observation, report.Inventory, report.Requirements, report.Source, row.SourceReference, row.RequirementRefs)
		} else {
			row.Status = lifecycleClassifyNoObservation(route, ambiguous, report.Observation, report.Inventory, report.Requirements, report.Source, row.SourceReference, row.RequirementRefs)
			row.Reasons = lifecycleRouteReasons(row.Status, route.selectable, ambiguous, report.Observation, report.Inventory, report.Requirements, report.Source, row.SourceReference, row.RequirementRefs)
		}
		report.Routes = append(report.Routes, row)
	}
	usageShapeCounts := map[routeLifecycleRouteKey]int{}
	for shape, rows := range usageByShape {
		usageShapeCounts[shape] = len(rows)
	}
	for _, key := range sortedLifecycleSourceKeys(source.current) {
		if matchedSource[key] {
			continue
		}
		sourceRoute := source.current[key]
		shapePath, shapeOK := lifecyclePathShape(sourceRoute.Path)
		shape := routeLifecycleRouteKey{method: strings.ToUpper(sourceRoute.Method), path: shapePath}
		contractShapePresent := shapeOK && contractCounts[shape] > 0
		contractUnknown := !contractSurfaceComplete || contractShapePresent
		row := routeLifecycleRoute{
			Method: strings.ToUpper(sourceRoute.Method), Path: sourceRoute.Path, Status: "source_only",
			Surfaces: []string{"current_source"}, SourceReference: "present", Reasons: []string{"static_source_route_not_in_captured_contract", "static_source_discovery_does_not_prove_runtime_presence"},
			RequirementRefs: []string{}, Selectable: lifecycleRouteSelectable(sourceRoute.Method, sourceRoute.Path),
		}
		applyLifecycleSourceRoute(&row, sourceRoute, "")
		if contractUnknown {
			row.Status = "unknown"
			row.Reasons = []string{"static_source_discovery_does_not_prove_runtime_presence"}
			if contractShapePresent {
				row.Reasons = append(row.Reasons, "source_contract_match_ambiguous")
			} else {
				row.Reasons = append(row.Reasons, "contract_inventory_incomplete")
			}
		}
		usageRow, usageMatch := lifecycleFindUsageForSourceRoute(sourceRoute, shape, shapeOK, contractCounts, sourceByShape, usageByKey, usageByShape, usageShapeCounts, contractUnknown)
		if usageRow != nil {
			applyLifecycleUsage(&row, *usageRow)
			row.ObservationMatch = usageMatch
			row.Surfaces = append(row.Surfaces, "observed_traffic")
			usedUsage[lifecycleUsageKey(*usageRow)] = true
			if !contractUnknown {
				row.Status = "source_and_observed_outside_contract"
				row.Reasons = []string{"observed_route_not_in_captured_contract", "static_source_route_match"}
				report.Inventory.ObservedRoutesOutsideDoc++
			}
		}
		if contractUnknown {
			row.SourceReference = "present"
		} else if row.Status == "source_only" {
			row.Reasons = []string{"static_source_route_not_in_captured_contract", "static_source_discovery_does_not_prove_runtime_presence"}
		}
		matchedSource[key] = true
		report.Routes = append(report.Routes, row)
	}
	if usage != nil {
		for _, usageRow := range usage.Routes {
			key := lifecycleUsageKey(usageRow)
			if usedUsage[key] {
				continue
			}
			shapePath, shapeOK := lifecyclePathShape(key.path)
			shape := routeLifecycleRouteKey{method: strings.ToUpper(usageRow.Method), path: shapePath}
			sourceRoute, sourceMatch, sourceAmbiguous := lifecycleFindSourceForUsage(key, shape, shapeOK, usageShapeCounts, source.current, sourceByShape)
			contractShapePresent := shapeOK && contractCounts[shape] > 0
			contractUnknown := !contractSurfaceComplete || contractShapePresent
			row := routeLifecycleRoute{
				Method: key.method, Path: key.path, Status: "observed_only", Surfaces: []string{"observed_traffic"},
				Reasons: []string{"observed_route_not_in_captured_contract"}, ObservationMatch: "observed_only",
				SourceReference: lifecycleSourceReferenceForPath(key, shape, shapeOK, source, sourceRefByShape),
				RequirementRefs: []string{}, Selectable: lifecycleRouteSelectable(usageRow.Method, key.path),
			}
			applyLifecycleUsage(&row, usageRow)
			if sourceRoute != nil {
				row.SourceReference = "present"
				row.Surfaces = append(row.Surfaces, "current_source")
				applyLifecycleSourceRoute(&row, *sourceRoute, sourceMatch)
			}
			sourceUnknown := source.status != "complete" && sourceRoute == nil
			if contractUnknown || sourceAmbiguous || sourceUnknown {
				row.Status = "unknown"
				row.Reasons = []string{"observed_route_surface_match_ambiguous"}
				if contractShapePresent {
					row.Reasons = append(row.Reasons, "observed_contract_match_ambiguous")
				} else if !contractSurfaceComplete {
					row.Reasons = append(row.Reasons, "contract_inventory_incomplete")
				}
				if sourceAmbiguous {
					row.Reasons = append(row.Reasons, "observed_source_match_ambiguous")
				} else if sourceUnknown {
					row.Reasons = append(row.Reasons, "current_source_inventory_incomplete")
				}
			} else {
				report.Inventory.ObservedRoutesOutsideDoc++
			}
			report.Routes = append(report.Routes, row)
		}
	}
	for index := range report.Routes {
		sort.Slice(report.Routes[index].Surfaces, func(i, j int) bool {
			return lifecycleSurfaceRank(report.Routes[index].Surfaces[i]) < lifecycleSurfaceRank(report.Routes[index].Surfaces[j])
		})
	}
	sort.Slice(report.Routes, func(i, j int) bool {
		if report.Routes[i].Method != report.Routes[j].Method {
			return report.Routes[i].Method < report.Routes[j].Method
		}
		return report.Routes[i].Path < report.Routes[j].Path
	})
	if report.Inventory.ObservedRoutesOutsideDoc > 0 {
		report.Inventory.Status, report.Inventory.Reason = "incomplete", "observed_routes_outside_contract"
		for index := range report.Routes {
			if report.Routes[index].Status == "review_candidate" {
				report.Routes[index].Status = "unknown"
				report.Routes[index].Reasons = []string{"observed_routes_outside_contract", "contract_inventory_incomplete"}
			}
		}
	}
	for _, route := range report.Routes {
		hasContract := slices.Contains(route.Surfaces, "captured_contract")
		hasSource := slices.Contains(route.Surfaces, "current_source")
		hasObservation := slices.Contains(route.Surfaces, "observed_traffic")
		report.Summary.ContractRoutes += boolInt(hasContract)
		report.Summary.SourceRoutes += boolInt(hasSource)
		report.Summary.ObservedRoutes += boolInt(route.Status == "active")
		report.Summary.ReviewCandidates += boolInt(route.Status == "review_candidate")
		report.Summary.ProtectedRoutes += boolInt(route.Status == "protected")
		report.Summary.UnknownRoutes += boolInt(route.Status == "unknown")
		report.Summary.ObservedOnlyRoutes += boolInt(route.Status == "observed_only")
		report.Summary.SourceOutsideContractRoutes += boolInt(!hasContract && hasSource && (route.Status == "source_only" || route.Status == "source_and_observed_outside_contract"))
		report.Summary.ContractMissingFromSourceRoutes += boolInt(hasContract && (route.SourceReference == "removed" || route.SourceReference == "not_found"))
		report.Summary.ObservedOutsideContractRoutes += boolInt(!hasContract && hasObservation && contractSurfaceComplete && !slices.Contains(route.Reasons, "observed_contract_match_ambiguous"))
	}
	complete := report.Inventory.Status == "available" && report.Observation.Status == "available" &&
		report.Source.Status == "complete" && !report.Observation.WindowClamped && !report.Observation.RoutesTruncated && report.Summary.UnknownRoutes == 0
	if !complete {
		report.Outcome = "incomplete"
	} else if report.Summary.ReviewCandidates > 0 || report.Summary.SourceOutsideContractRoutes > 0 ||
		report.Summary.ContractMissingFromSourceRoutes > 0 || report.Summary.ObservedOutsideContractRoutes > 0 {
		report.Outcome = "review_required"
	} else {
		report.Outcome = "clear"
	}
	report.Caveats = routeLifecycleCaveats(report)
	return report
}

func routeLifecycleContractRoutes(spec *openapidiff.Spec) ([]routeLifecycleContractRoute, int) {
	routes := []routeLifecycleContractRoute{}
	if spec == nil {
		return routes, 0
	}
	zero := int64(0)
	for path, item := range spec.Paths {
		if item == nil {
			continue
		}
		for method := range item.Methods {
			method = strings.ToUpper(method)
			key := routeLifecycleRouteKey{method: method, path: path}
			selectorErr := routehealth.Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: method, Path: path}}})
			shapePath, shapeOK := routeLifecyclePathShape(path)
			routes = append(routes, routeLifecycleContractRoute{
				method: method, path: path, key: key,
				shape:      routeLifecycleRouteKey{method: method, path: shapePath},
				selectable: selectorErr == nil && shapeOK,
			})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].method != routes[j].method {
			return routes[i].method < routes[j].method
		}
		return routes[i].path < routes[j].path
	})
	unselectable := 0
	for _, route := range routes {
		if !route.selectable {
			unselectable++
		}
	}
	return routes, unselectable
}

func lifecyclePathShape(path string) (string, bool) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return "", false
	}
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if !strings.ContainsAny(segment, "{}") {
			continue
		}
		if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' || strings.ContainsAny(segment[1:len(segment)-1], "{}") {
			return "", false
		}
		segments[index] = "{}"
	}
	return strings.Join(segments, "/"), true
}

func routeLifecyclePathShape(path string) (string, bool) { return lifecyclePathShape(path) }

func lifecycleUsageIndexes(contract []routeLifecycleContractRoute, usage *api.RouteCustomerUsageResponse) (map[routeLifecycleRouteKey]int, map[routeLifecycleRouteKey]*api.RouteCustomerUsage, map[routeLifecycleRouteKey][]*api.RouteCustomerUsage) {
	contractCounts := map[routeLifecycleRouteKey]int{}
	for _, route := range contract {
		contractCounts[route.shape]++
	}
	byKey := map[routeLifecycleRouteKey]*api.RouteCustomerUsage{}
	byShape := map[routeLifecycleRouteKey][]*api.RouteCustomerUsage{}
	if usage != nil {
		for i := range usage.Routes {
			row := &usage.Routes[i]
			key := lifecycleUsageKey(*row)
			path := key.path
			shapePath, ok := lifecyclePathShape(path)
			if !ok {
				continue
			}
			byKey[key] = row
			shape := routeLifecycleRouteKey{method: key.method, path: shapePath}
			byShape[shape] = append(byShape[shape], row)
		}
	}
	return contractCounts, byKey, byShape
}

func lifecycleUsageKey(usage api.RouteCustomerUsage) routeLifecycleRouteKey {
	method := strings.ToUpper(usage.Method)
	path := usage.Route
	if prefix, remainder, ok := strings.Cut(path, " "); ok && strings.EqualFold(prefix, usage.Method) {
		path = remainder
	}
	return routeLifecycleRouteKey{method: method, path: path}
}

func lifecycleRouteSelectable(method, path string) bool {
	if _, ok := lifecyclePathShape(path); !ok {
		return false
	}
	zero := int64(0)
	return routehealth.Validate(api.SetRouteHealthGateRequest{
		Mode: "report", ExpectedRevision: &zero,
		Routes: []api.RouteHealthRoute{{Method: strings.ToUpper(method), Path: path}},
	}) == nil
}

func lifecycleSurfaceRank(surface string) int {
	switch surface {
	case "captured_contract":
		return 0
	case "current_source":
		return 1
	case "observed_traffic":
		return 2
	default:
		return 3
	}
}

func lifecycleFindUsage(route routeLifecycleContractRoute, contractCounts map[routeLifecycleRouteKey]int, byKey map[routeLifecycleRouteKey]*api.RouteCustomerUsage, byShape map[routeLifecycleRouteKey][]*api.RouteCustomerUsage) (*api.RouteCustomerUsage, string, bool) {
	if row := byKey[route.key]; row != nil {
		return row, "exact", false
	}
	rows := byShape[route.shape]
	if len(rows) == 0 {
		return nil, "", false
	}
	if len(rows) == 1 && contractCounts[route.shape] == 1 {
		return rows[0], "parameter_shape", false
	}
	return nil, "", true
}

func lifecycleCurrentSourceShapes(routes map[routeLifecycleRouteKey]routeimpact.Route) map[routeLifecycleRouteKey][]routeimpact.Route {
	result := map[routeLifecycleRouteKey][]routeimpact.Route{}
	for _, route := range routes {
		shapePath, ok := lifecyclePathShape(route.Path)
		if !ok {
			continue
		}
		shape := routeLifecycleRouteKey{method: strings.ToUpper(route.Method), path: shapePath}
		result[shape] = append(result[shape], route)
	}
	return result
}

func sortedLifecycleSourceKeys(routes map[routeLifecycleRouteKey]routeimpact.Route) []routeLifecycleRouteKey {
	keys := make([]routeLifecycleRouteKey, 0, len(routes))
	for key := range routes {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		return keys[i].path < keys[j].path
	})
	return keys
}

func lifecycleFindSourceRoute(route routeLifecycleContractRoute, contractCounts map[routeLifecycleRouteKey]int, current map[routeLifecycleRouteKey]routeimpact.Route, byShape map[routeLifecycleRouteKey][]routeimpact.Route) (*routeimpact.Route, string, bool) {
	if currentRoute, ok := current[route.key]; ok {
		return &currentRoute, "exact", false
	}
	if route.shape.path == "" {
		return nil, "", false
	}
	rows := byShape[route.shape]
	if len(rows) == 0 {
		return nil, "", false
	}
	if len(rows) == 1 && contractCounts[route.shape] == 1 {
		currentRoute := rows[0]
		return &currentRoute, "parameter_shape", false
	}
	return nil, "", true
}

func lifecycleFindUsageForSourceRoute(
	route routeimpact.Route, shape routeLifecycleRouteKey, shapeOK bool,
	contractCounts map[routeLifecycleRouteKey]int, sourceByShape map[routeLifecycleRouteKey][]routeimpact.Route,
	usageByKey map[routeLifecycleRouteKey]*api.RouteCustomerUsage, usageByShape map[routeLifecycleRouteKey][]*api.RouteCustomerUsage,
	usageShapeCounts map[routeLifecycleRouteKey]int, contractAmbiguous bool,
) (*api.RouteCustomerUsage, string) {
	key := routeLifecycleRouteKey{method: strings.ToUpper(route.Method), path: route.Path}
	if usage := usageByKey[key]; usage != nil {
		return usage, "exact"
	}
	if contractAmbiguous || !shapeOK || contractCounts[shape] > 0 || len(sourceByShape[shape]) != 1 || usageShapeCounts[shape] != 1 {
		return nil, ""
	}
	rows := usageByShape[shape]
	if len(rows) == 1 {
		return rows[0], "parameter_shape"
	}
	return nil, ""
}

func lifecycleFindSourceForUsage(key, shape routeLifecycleRouteKey, shapeOK bool, usageShapeCounts map[routeLifecycleRouteKey]int, current map[routeLifecycleRouteKey]routeimpact.Route, byShape map[routeLifecycleRouteKey][]routeimpact.Route) (*routeimpact.Route, string, bool) {
	if sourceRoute, ok := current[key]; ok {
		return &sourceRoute, "exact", false
	}
	if !shapeOK {
		return nil, "", false
	}
	rows := byShape[shape]
	if len(rows) == 1 && usageShapeCounts[shape] == 1 {
		sourceRoute := rows[0]
		return &sourceRoute, "parameter_shape", false
	}
	return nil, "", len(rows) > 1 || len(rows) == 1 && usageShapeCounts[shape] > 1
}

func lifecycleSourceReferenceForPath(key, shape routeLifecycleRouteKey, shapeOK bool, source routeLifecycleSourceEvidence, byShape map[routeLifecycleRouteKey][]routeLifecycleSourceRef) string {
	contractRoute := routeLifecycleContractRoute{key: key}
	if shapeOK {
		contractRoute.shape = shape
	}
	return lifecycleSourceReference(contractRoute, source, byShape)
}

func applyLifecycleSourceRoute(row *routeLifecycleRoute, route routeimpact.Route, match string) {
	row.SourceMatch = match
	row.SourcePath = route.Path
	row.SourceHandler = route.Handler
	row.SourceFile, row.SourceLine = route.Source.File, route.Source.Line
	row.RegistrationFile, row.RegistrationLine = route.Registration.File, route.Registration.Line
}

func applyLifecycleUsage(row *routeLifecycleRoute, usage api.RouteCustomerUsage) {
	row.Requests, row.Tenants, row.Consumers = usage.Requests, usage.PlatformTenantCount, usage.ConsumerCount
	row.LastObservedAt = usage.LastObservedAt
}

func lifecycleClassifyNoObservation(route routeLifecycleContractRoute, ambiguous bool, observation routeLifecycleObservation, inventory routeLifecycleInventory, requirements routeLifecycleRequirements, source routeLifecycleSource, sourceReference string, requirementRefs []string) string {
	if sourceReference == "present" || len(requirementRefs) > 0 {
		return "protected"
	}
	if !route.selectable || ambiguous || inventory.Status != "available" || observation.Status != "available" ||
		observation.WindowClamped || observation.RoutesTruncated || requirements.Status == "unavailable" || source.Status != "complete" {
		return "unknown"
	}
	return "review_candidate"
}

func lifecycleRouteReasons(status string, selectable, ambiguous bool, observation routeLifecycleObservation, inventory routeLifecycleInventory, requirements routeLifecycleRequirements, source routeLifecycleSource, sourceReference string, requirementRefs []string) []string {
	if status == "active" {
		reasons := []string{"observed_in_window"}
		switch sourceReference {
		case "removed":
			reasons = append(reasons, "observed_route_removed_from_bound_source_snapshot")
		case "not_found":
			reasons = append(reasons, "observed_route_not_found_in_bound_source_snapshot")
		}
		if ambiguous {
			reasons = append(reasons, "source_route_match_ambiguous")
		}
		return reasons
	}
	if status == "observed_only" {
		return []string{"observed_route_not_in_captured_contract"}
	}
	reasons := []string{}
	if !selectable {
		reasons = append(reasons, "contract_route_not_supported_by_telemetry")
	}
	if ambiguous {
		reasons = append(reasons, "route_match_ambiguous")
	}
	if inventory.Status != "available" {
		reasons = append(reasons, inventory.Reason)
	}
	if observation.Status != "available" {
		reasons = append(reasons, observation.Reason)
	}
	if observation.WindowClamped {
		reasons = append(reasons, "observation_window_clamped")
	}
	if observation.RoutesTruncated {
		reasons = append(reasons, "observation_route_inventory_truncated")
	}
	if requirements.Status == "unavailable" {
		reasons = append(reasons, "saved_requirements_unavailable")
	}
	if source.Status != "complete" {
		reasons = append(reasons, "current_source_binding_incomplete")
	}
	if sourceReference == "present" {
		reasons = append(reasons, "route_still_declared_in_deployed_source")
	}
	if sourceReference == "removed" {
		reasons = append(reasons, "route_removed_from_deployed_source_snapshot")
	}
	if sourceReference == "not_found" {
		reasons = append(reasons, "contract_route_not_found_in_bound_source_snapshot")
	}
	if len(requirementRefs) > 0 {
		reasons = append(reasons, "route_has_saved_requirement_references")
	}
	if status == "review_candidate" {
		reasons = append(reasons, "no_recent_telemetry_or_static_source_reference")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "no_recent_observation_is_not_proof_of_unused")
	}
	return slices.Compact(reasons)
}

func lifecycleSourceShapes(refs map[routeLifecycleRouteKey]routeLifecycleSourceRef) map[routeLifecycleRouteKey][]routeLifecycleSourceRef {
	result := map[routeLifecycleRouteKey][]routeLifecycleSourceRef{}
	for key, ref := range refs {
		shapePath, ok := lifecyclePathShape(key.path)
		if !ok {
			continue
		}
		shape := routeLifecycleRouteKey{method: key.method, path: shapePath}
		result[shape] = append(result[shape], ref)
	}
	return result
}

func lifecycleSourceReference(route routeLifecycleContractRoute, source routeLifecycleSourceEvidence, byShape map[routeLifecycleRouteKey][]routeLifecycleSourceRef) string {
	if !source.bound {
		return "unknown"
	}
	ref, exists := source.refs[route.key]
	if !exists {
		refs := byShape[route.shape]
		if len(refs) == 1 {
			ref, exists = refs[0], true
		} else if len(refs) > 1 {
			return "unknown"
		}
	}
	if exists && ref.present {
		return "present"
	}
	if source.status == "complete" {
		if exists && ref.removed {
			return "removed"
		}
		return "not_found"
	}
	return "unknown"
}

func lifecycleRequirementReferences(route routeLifecycleContractRoute, config *api.RouteRequirementsConfig) []string {
	refs := []string{}
	if config == nil {
		return refs
	}
	for _, requirement := range config.Routes {
		if strings.ToUpper(requirement.Method) == route.method && requirement.Path == route.path {
			label := requirement.Name
			if label == "" {
				label = route.method + " " + route.path
			}
			refs = append(refs, "route:"+label)
		}
	}
	for _, exception := range config.Public {
		if strings.ToUpper(exception.Method) == route.method && exception.Path == route.path {
			refs = append(refs, "public:"+route.method+" "+route.path)
		}
	}
	for _, group := range config.Groups {
		if slices.ContainsFunc(group.Methods, func(method string) bool { return strings.ToUpper(method) == route.method }) && strings.HasPrefix(route.path, group.PathPrefix) {
			refs = append(refs, "group:"+group.Name)
		}
	}
	sort.Strings(refs)
	return slices.Compact(refs)
}

func routeLifecycleCaveats(report routeLifecycleReviewReport) []string {
	caveats := []string{
		"No route telemetry is observed_only evidence. A review candidate is not proof that an endpoint is unused and is never an instruction to delete it.",
		"The deployed OpenAPI document is a contract inventory, not a complete runtime route registry. Observed routes outside it make inventory coverage incomplete.",
		"A static source route missing from the captured contract is a source/contract discrepancy; static discovery alone does not prove the route is reachable in the deployed runtime.",
		"Customer and consumer identifiers are omitted; tenant and consumer counts are overlapping groups and must not be added together.",
	}
	if report.Observation.WindowClamped {
		caveats = append(caveats, "The requested observation window was clamped to the plan's retained telemetry window; unobserved routes are inconclusive.")
	}
	if report.Observation.RoutesTruncated {
		caveats = append(caveats, "The observed route list was truncated; routes absent from the returned list are inconclusive.")
	}
	if report.Source.Status != "complete" {
		caveats = append(caveats, "Current source references could not be completely bound to the deployed commit; routes without observations are not retirement candidates.")
	}
	if report.Requirements.Status == "unavailable" {
		caveats = append(caveats, "Saved route requirements could not be verified; routes without observations are not retirement candidates.")
	}
	if report.Inventory.Status != "available" {
		caveats = append(caveats, "The deployed contract inventory is incomplete; routes without observations are not retirement candidates.")
	}
	return caveats
}

func renderRouteLifecycleReview(w io.Writer, report routeLifecycleReviewReport) {
	_, _ = fmt.Fprintf(w, "Route lifecycle review: %s\nApp: %s; deployment: %s\nObservation: %s to %s (requested %s; %s)\nInventory: %s (%d contract routes); source: %s (%d current routes); saved requirements: %s\nSummary: %d observed, %d review candidates, %d protected, %d unknown; %d source routes outside contract, %d contract routes missing from source, %d observed routes outside contract\n",
		report.Outcome, previewReportText(report.App), previewReportText(report.DeploymentID),
		previewReportText(report.Observation.From), previewReportText(report.Observation.Until), report.Observation.RequestedSince,
		report.Observation.Status, report.Inventory.Status, report.Inventory.ContractRoutes,
		report.Source.Status, report.Source.CurrentRoutes, report.Requirements.Status, report.Summary.ObservedRoutes,
		report.Summary.ReviewCandidates, report.Summary.ProtectedRoutes, report.Summary.UnknownRoutes,
		report.Summary.SourceOutsideContractRoutes, report.Summary.ContractMissingFromSourceRoutes, report.Summary.ObservedOutsideContractRoutes)
	for _, route := range report.Routes {
		if route.Status == "active" && route.SourceReference != "removed" && route.SourceReference != "not_found" && !slices.Contains(route.Reasons, "source_route_match_ambiguous") {
			continue
		}
		_, _ = fmt.Fprintf(w, "\n%s %s: %s (%s)", previewReportText(route.Method), previewReportText(route.Path), route.Status, strings.Join(route.Reasons, ", "))
		if route.LastObservedAt != "" {
			_, _ = fmt.Fprintf(w, "; last observed %s, %d requests, %d tenants, %d consumers", route.LastObservedAt, route.Requests, route.Tenants, route.Consumers)
		}
		if route.SourceReference != "unknown" {
			_, _ = fmt.Fprintf(w, "; source %s", route.SourceReference)
		}
		if route.SourcePath != "" && route.SourcePath != route.Path {
			_, _ = fmt.Fprintf(w, "; source path %s", previewReportText(route.SourcePath))
		}
		if route.SourceHandler != "" {
			_, _ = fmt.Fprintf(w, "; handler %s", previewReportText(route.SourceHandler))
		}
		if route.SourceFile != "" {
			_, _ = fmt.Fprintf(w, "; source location %s:%d", previewReportText(route.SourceFile), route.SourceLine)
		}
		if route.RegistrationFile != "" {
			_, _ = fmt.Fprintf(w, "; registered at %s:%d", previewReportText(route.RegistrationFile), route.RegistrationLine)
		}
		if len(route.RequirementRefs) > 0 {
			_, _ = fmt.Fprintf(w, "; requirements %s", strings.Join(route.RequirementRefs, ", "))
		}
		_, _ = fmt.Fprintln(w)
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "\nEvidence: %s\n", caveat)
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
