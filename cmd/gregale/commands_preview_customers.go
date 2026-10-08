package main

import (
	"bytes"
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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const previewCustomerRosterVersion = 1

type previewCustomerRosterSummary struct {
	PreviewReports              int `json:"preview_reports"`
	CustomerEvidenceAvailable   int `json:"customer_evidence_available"`
	CustomerEvidenceUnavailable int `json:"customer_evidence_unavailable"`
	AddedRoutesOmitted          int `json:"added_routes_omitted"`
	ChangedRoutes               int `json:"changed_routes"`
	ObservedChangedRoutes       int `json:"observed_changed_routes"`
	UnobservedChangedRoutes     int `json:"unobserved_changed_routes"`
	Customers                   int `json:"customers"`
	IncompleteCustomers         int `json:"incomplete_customers"`
}

type previewCustomerRosterReport struct {
	Version     int                          `json:"version"`
	GeneratedAt time.Time                    `json:"generated_at"`
	GroupBy     string                       `json:"group_by"`
	Status      string                       `json:"status"`
	Summary     previewCustomerRosterSummary `json:"summary"`
	Customers   []previewCustomerRosterEntry `json:"customers"`
	Caveats     []string                     `json:"caveats"`
}

type previewCustomerRosterEntry struct {
	ID                   string                       `json:"id"`
	IdentityScope        string                       `json:"identity_scope"`
	App                  string                       `json:"app,omitempty"`
	Previews             []string                     `json:"previews"`
	AffectedRoutes       int                          `json:"affected_routes"`
	BreakingRoutes       int                          `json:"breaking_routes"`
	ObservedRequests     int64                        `json:"observed_requests"`
	LastObservedAt       string                       `json:"last_observed_at,omitempty"`
	Incomplete           bool                         `json:"incomplete"`
	IncompleteReasons    []string                     `json:"incomplete_reasons,omitempty"`
	Routes               []previewCustomerRosterRoute `json:"routes"`
	requestCountsByRoute map[string]int64             `json:"-"`
	requestTimesByRoute  map[string]string            `json:"-"`
	breakingByRoute      map[string]bool              `json:"-"`
	routeIndexes         map[string]int               `json:"-"`
	previewSet           map[string]struct{}          `json:"-"`
	reasonSet            map[string]struct{}          `json:"-"`
}

type previewCustomerRosterRoute struct {
	Preview                  string   `json:"preview"`
	App                      string   `json:"app"`
	Method                   string   `json:"method"`
	Path                     string   `json:"path"`
	Change                   string   `json:"change"`
	Reasons                  []string `json:"reasons"`
	Breaking                 bool     `json:"breaking"`
	ObservedRequests         int64    `json:"observed_requests"`
	LastObservedAt           string   `json:"last_observed_at,omitempty"`
	CustomerDetailsTruncated bool     `json:"customer_details_truncated,omitempty"`
	OmittedCustomerRequests  int64    `json:"omitted_customer_requests,omitempty"`
	Incomplete               bool     `json:"incomplete,omitempty"`
}

type previewCustomerRosterInput struct {
	reports        []previewRouteReport
	previewReports int
	unavailable    int
	generatedAt    time.Time
}

type previewCustomerRosterIdentity struct {
	scope string
	id    string
}

func cmdPreviewCustomers(args []string) int {
	if len(args) > 0 && args[0] == "track" {
		return cmdPreviewCustomersTrack(args[1:])
	}
	if len(args) > 0 && args[0] == "progress" {
		return cmdPreviewCustomersProgress(args[1:])
	}
	if len(args) > 0 && args[0] == "migration" {
		return cmdPreviewCustomersMigration(args[1:])
	}
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("preview customers", flag.ContinueOnError)
	reportPath := fs.String("report", "", "single preview or release report JSON file")
	groupBy := fs.String("by", "consumer", "group affected routes by consumer or tenant")
	format := fs.String("format", "text", "report format: text, markdown, or csv (or use --json)")
	output := fs.String("out", "", "write the machine-readable roster to a new JSON file")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || *reportPath == "" || !slices.Contains([]string{"consumer", "tenant"}, *groupBy) ||
		!slices.Contains([]string{"text", "markdown", "csv"}, *format) || (jsonOutput && *format != "text") || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(osStderr, "usage: gregale preview customers --report <PATH> [--by consumer|tenant] [--format text|markdown|csv] [--out <PATH>] [--json]", "preview")
		return 1
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	input, err := readPreviewCustomerRosterInput(*reportPath)
	if err != nil {
		return printErr("Could not read preview customer evidence", err)
	}
	report, err := buildPreviewCustomerRoster(input, *groupBy)
	if err != nil {
		return printErr("Could not build preview customer roster", err)
	}
	if *output != "" {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return printErr("Could not encode customer roster", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save customer roster", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	var rendered bytes.Buffer
	switch *format {
	case "markdown":
		renderPreviewCustomerRosterMarkdown(&rendered, report)
	case "csv":
		if err := renderPreviewCustomerRosterCSV(&rendered, report); err != nil {
			return printErr("Could not render customer roster", err)
		}
	default:
		renderPreviewCustomerRosterText(&rendered, report)
	}
	if _, err := osStdout.Write(rendered.Bytes()); err != nil {
		return printErr("Could not write customer roster", err)
	}
	return 0
}

func readPreviewCustomerRosterInput(path string) (previewCustomerRosterInput, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return previewCustomerRosterInput{}, errors.New("use a readable regular report file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil {
		return previewCustomerRosterInput{}, errors.New("read report file failed")
	}
	if int64(len(body)) > api.RouteImpactReportMaxBytes {
		return previewCustomerRosterInput{}, errors.New("report file exceeds the 64 MiB limit")
	}
	var header struct {
		Version  int             `json:"version"`
		Preview  string          `json:"preview"`
		Previews json.RawMessage `json:"previews"`
	}
	if err := json.Unmarshal(body, &header); err != nil {
		return previewCustomerRosterInput{}, errors.New("report must be valid preview report JSON")
	}
	if len(header.Previews) != 0 && string(header.Previews) != "null" {
		var release previewReleaseRouteReview
		if err := json.Unmarshal(body, &release); err != nil || release.Version != 1 || len(release.Previews) == 0 {
			return previewCustomerRosterInput{}, errors.New("release report must be a supported gregale preview review JSON report")
		}
		input := previewCustomerRosterInput{previewReports: len(release.Previews), generatedAt: release.GeneratedAt}
		for _, preview := range release.Previews {
			if preview.Report == nil {
				input.unavailable++
				continue
			}
			input.reports = append(input.reports, *preview.Report)
		}
		return input, nil
	}
	var report previewRouteReport
	if err := json.Unmarshal(body, &report); err != nil || report.Version < 5 || report.Version > 7 || report.Preview == "" {
		return previewCustomerRosterInput{}, errors.New("input must be a preview report with observed customer evidence (version 5 through 7)")
	}
	return previewCustomerRosterInput{reports: []previewRouteReport{report}, previewReports: 1, generatedAt: report.GeneratedAt}, nil
}

func buildPreviewCustomerRoster(input previewCustomerRosterInput, groupBy string) (previewCustomerRosterReport, error) {
	if groupBy != "consumer" && groupBy != "tenant" {
		return previewCustomerRosterReport{}, errors.New("grouping must be consumer or tenant")
	}
	if input.previewReports == 0 {
		return previewCustomerRosterReport{}, errors.New("report contains no preview entries")
	}
	result := previewCustomerRosterReport{
		Version: previewCustomerRosterVersion, GeneratedAt: input.generatedAt.UTC(), GroupBy: groupBy,
		Status: "advisory", Customers: []previewCustomerRosterEntry{}, Caveats: []string{
			"This roster reflects retained, observed baseline traffic only. It does not prove that an identified customer will break or that missing traffic is unused.",
			"Customer details and route inventories are bounded. Truncated or unavailable evidence is marked incomplete; customer IDs are opaque and are not resolved to names.",
			"For multiple previews of the same app and route, observed request counts use the largest reported per-route count to avoid double-counting shared baseline traffic. Totals across routes are a prioritization aid, not a unique customer or billing count.",
			"Newly added routes have no baseline customer exposure and are counted separately rather than treated as affected existing customers.",
		}}
	result.Summary.PreviewReports = input.previewReports
	result.Summary.CustomerEvidenceUnavailable = input.unavailable
	customers := map[previewCustomerRosterIdentity]*previewCustomerRosterEntry{}
	changedRoutes := map[string]struct{}{}
	addedRoutes := map[string]struct{}{}
	observedChangedRoutes := map[string]struct{}{}
	unobservedChangedRoutes := map[string]struct{}{}
	var latestGenerated time.Time
	for _, source := range input.reports {
		if source.GeneratedAt.After(latestGenerated) {
			latestGenerated = source.GeneratedAt
		}
		if source.Customers.Status != "advisory" || !source.Customers.DetailsIncluded {
			result.Summary.CustomerEvidenceUnavailable++
			result.Status = "incomplete"
			continue
		}
		result.Summary.CustomerEvidenceAvailable++
		reportIncomplete := source.Customers.WindowClamped || source.Customers.RoutesTruncated
		for _, route := range source.Routes {
			if route.Change == "added" {
				addedRoutes[source.Preview+"\x00"+previewReportRouteKey(route.Method, route.Path)] = struct{}{}
				continue
			}
			reasons, breaking := previewCustomerRouteChanges(route)
			if len(reasons) == 0 {
				continue
			}
			routeIdentity := source.Preview + "\x00" + previewReportRouteKey(route.Method, route.Path)
			changedRoutes[routeIdentity] = struct{}{}
			impact := route.CustomerImpact
			if impact == nil || impact.Status != "observed" || impact.Usage == nil || impact.Usage.Customers == nil {
				unobservedChangedRoutes[routeIdentity] = struct{}{}
				result.Status = "incomplete"
				continue
			}
			usage := impact.Usage
			grouped := map[string]api.RouteCustomerObservation{}
			for _, observation := range usage.Customers {
				id := observation.ConsumerID
				if groupBy == "tenant" {
					id = observation.PlatformTenantID
				}
				parsed, err := uuid.Parse(id)
				if err != nil || parsed == uuid.Nil {
					continue
				}
				id = parsed.String()
				current := grouped[id]
				current.Requests += observation.Requests
				current.LastObservedAt = laterTimestamp(current.LastObservedAt, observation.LastObservedAt)
				grouped[id] = current
			}
			expectedGroups := usage.ConsumerCount
			if groupBy == "tenant" {
				expectedGroups = usage.PlatformTenantCount
			}
			groupsIncomplete := int64(len(grouped)) < expectedGroups
			routeIncomplete := reportIncomplete || usage.CustomersTruncated || groupsIncomplete || usage.AnonymousRequests > 0 || usage.UnresolvedIdentityRequests > 0
			if routeIncomplete {
				result.Status = "incomplete"
			}
			if len(grouped) == 0 {
				unobservedChangedRoutes[routeIdentity] = struct{}{}
			}
			for id, observation := range grouped {
				key := previewCustomerRosterIdentity{id: id}
				if groupBy == "consumer" {
					key.scope = source.Parent
				}
				entry := customers[key]
				if entry == nil {
					identityScope := "app"
					if groupBy == "tenant" {
						identityScope = "account"
					}
					entry = &previewCustomerRosterEntry{
						ID: id, IdentityScope: identityScope, App: key.scope, Routes: []previewCustomerRosterRoute{},
						requestCountsByRoute: map[string]int64{}, requestTimesByRoute: map[string]string{},
						breakingByRoute: map[string]bool{}, routeIndexes: map[string]int{}, previewSet: map[string]struct{}{}, reasonSet: map[string]struct{}{},
					}
					customers[key] = entry
				}
				entry.previewSet[source.Preview] = struct{}{}
				exposureKey := source.Parent + "\x00" + previewReportRouteKey(route.Method, route.Path)
				entry.requestCountsByRoute[exposureKey] = max(entry.requestCountsByRoute[exposureKey], observation.Requests)
				entry.requestTimesByRoute[exposureKey] = laterTimestamp(entry.requestTimesByRoute[exposureKey], observation.LastObservedAt)
				entry.LastObservedAt = laterTimestamp(entry.LastObservedAt, observation.LastObservedAt)
				routeKey := source.Preview + "\x00" + exposureKey
				routeIndex, exists := entry.routeIndexes[routeKey]
				if !exists {
					routeIndex = len(entry.Routes)
					entry.routeIndexes[routeKey] = routeIndex
					entry.Routes = append(entry.Routes, previewCustomerRosterRoute{
						Preview: source.Preview, App: source.Parent, Method: route.Method, Path: route.Path,
						Change: route.Change, Reasons: []string{}, Breaking: breaking,
					})
				}
				outRoute := &entry.Routes[routeIndex]
				outRoute.Breaking = outRoute.Breaking || breaking
				outRoute.ObservedRequests = max(outRoute.ObservedRequests, observation.Requests)
				outRoute.LastObservedAt = laterTimestamp(outRoute.LastObservedAt, observation.LastObservedAt)
				if usage.CustomersTruncated {
					outRoute.CustomerDetailsTruncated = true
					outRoute.OmittedCustomerRequests = max(outRoute.OmittedCustomerRequests, usage.OtherCustomerRequests)
				}
				outRoute.Incomplete = outRoute.Incomplete || routeIncomplete
				for _, reason := range reasons {
					if !containsString(outRoute.Reasons, reason) {
						outRoute.Reasons = append(outRoute.Reasons, reason)
					}
				}
				if routeIncomplete {
					entry.Incomplete = true
					if reportIncomplete {
						entry.reasonSet["traffic_window_clamped_or_route_inventory_truncated"] = struct{}{}
					}
					if usage.CustomersTruncated {
						entry.reasonSet["customer_groups_truncated_for_route"] = struct{}{}
					}
					if usage.AnonymousRequests > 0 {
						entry.reasonSet["anonymous_requests_cannot_be_mapped_to_identity"] = struct{}{}
					}
					if usage.UnresolvedIdentityRequests > 0 {
						entry.reasonSet["unresolved_identity_requests_cannot_be_mapped_to_identity"] = struct{}{}
					}
					if groupsIncomplete {
						entry.reasonSet["selected_identity_groups_missing_from_bounded_details"] = struct{}{}
					}
				}
				entry.breakingByRoute[exposureKey] = entry.breakingByRoute[exposureKey] || breaking
				sort.Strings(outRoute.Reasons)
			}
			if len(grouped) > 0 {
				observedChangedRoutes[routeIdentity] = struct{}{}
			}
		}
	}
	if result.Summary.CustomerEvidenceAvailable == 0 {
		return previewCustomerRosterReport{}, errors.New("customer IDs were not included in the source report; generate it with --customer-details, then retry")
	}
	result.Summary.ChangedRoutes = len(changedRoutes)
	result.Summary.AddedRoutesOmitted = len(addedRoutes)
	result.Summary.ObservedChangedRoutes = len(observedChangedRoutes)
	result.Summary.UnobservedChangedRoutes = len(unobservedChangedRoutes)
	for _, entry := range customers {
		entry.Previews = make([]string, 0, len(entry.previewSet))
		for preview := range entry.previewSet {
			entry.Previews = append(entry.Previews, preview)
		}
		sort.Strings(entry.Previews)
		entry.IncompleteReasons = make([]string, 0, len(entry.reasonSet))
		for reason := range entry.reasonSet {
			entry.IncompleteReasons = append(entry.IncompleteReasons, reason)
		}
		sort.Strings(entry.IncompleteReasons)
		for _, requests := range entry.requestCountsByRoute {
			entry.ObservedRequests += requests
		}
		for _, last := range entry.requestTimesByRoute {
			entry.LastObservedAt = laterTimestamp(entry.LastObservedAt, last)
		}
		sort.Slice(entry.Routes, func(i, j int) bool {
			a, b := entry.Routes[i], entry.Routes[j]
			if a.Breaking != b.Breaking {
				return a.Breaking
			}
			if a.ObservedRequests != b.ObservedRequests {
				return a.ObservedRequests > b.ObservedRequests
			}
			if a.Preview != b.Preview {
				return a.Preview < b.Preview
			}
			return previewReportRouteKey(a.Method, a.Path) < previewReportRouteKey(b.Method, b.Path)
		})
		for _, breaking := range entry.breakingByRoute {
			if breaking {
				entry.BreakingRoutes++
			}
		}
		entry.AffectedRoutes = len(entry.requestCountsByRoute)
		entry.requestCountsByRoute, entry.requestTimesByRoute, entry.breakingByRoute, entry.routeIndexes = nil, nil, nil, nil
		entry.previewSet, entry.reasonSet = nil, nil
		if entry.Incomplete {
			result.Summary.IncompleteCustomers++
		}
		result.Customers = append(result.Customers, *entry)
	}
	result.Summary.Customers = len(result.Customers)
	sort.Slice(result.Customers, func(i, j int) bool {
		a, b := result.Customers[i], result.Customers[j]
		if a.BreakingRoutes != b.BreakingRoutes {
			return a.BreakingRoutes > b.BreakingRoutes
		}
		if a.AffectedRoutes != b.AffectedRoutes {
			return a.AffectedRoutes > b.AffectedRoutes
		}
		if a.ObservedRequests != b.ObservedRequests {
			return a.ObservedRequests > b.ObservedRequests
		}
		if a.LastObservedAt != b.LastObservedAt {
			return laterRFC3339(a.LastObservedAt, b.LastObservedAt)
		}
		if a.App != b.App {
			return a.App < b.App
		}
		return a.ID < b.ID
	})
	if result.GeneratedAt.IsZero() {
		result.GeneratedAt = latestGenerated
	}
	if result.Summary.CustomerEvidenceUnavailable > 0 || result.Summary.UnobservedChangedRoutes > 0 || result.Summary.IncompleteCustomers > 0 {
		result.Status = "incomplete"
		result.Caveats = append(result.Caveats, "Some changed routes or previews have unavailable, unobserved, or truncated customer evidence; absence from this roster does not mean a customer is unaffected.")
	}
	return result, nil
}

func previewCustomerRouteChanges(route previewReportRoute) ([]string, bool) {
	reasons := []string{}
	if route.Change != "" && route.Change != "unchanged" && route.Change != "unknown" {
		reasons = append(reasons, "contract_"+route.Change)
	}
	for _, change := range route.Breaks {
		if change.Kind != "" {
			reasons = append(reasons, "response_"+change.Kind)
		}
	}
	if route.RequestContractChanged && route.RequestCompatibility != nil {
		reasons = append(reasons, "request_compatibility_"+route.RequestCompatibility.Status)
	}
	if route.SecurityCompatibility != nil && route.SecurityCompatibility.Changed {
		reasons = append(reasons, "security_compatibility_"+route.SecurityCompatibility.Status)
	}
	if route.PolicyDrift != nil && route.PolicyDrift.Status == "changed" {
		reasons = append(reasons, "policy_drift_changed")
	}
	if route.SourceImpact != nil && route.SourceImpact.Change != "" && route.SourceImpact.Change != "no_linked_changes" && route.SourceImpact.Change != "unchanged" {
		reasons = append(reasons, "source_"+route.SourceImpact.Change)
	}
	sort.Strings(reasons)
	breaking := route.Change == "removed" || len(route.Breaks) > 0 ||
		(route.RequestCompatibility != nil && route.RequestCompatibility.Status == "breaking") ||
		(route.SecurityCompatibility != nil && route.SecurityCompatibility.ClientBreaking)
	return reasons, breaking
}

func renderPreviewCustomerRosterText(w io.Writer, report previewCustomerRosterReport) {
	_, _ = fmt.Fprintf(w, "Preview customer impact roster (%s; %s)\nStatus: %s; reports: %d; customers: %d; breaking customer-route links: %d; added routes omitted: %d; evidence-unavailable reports: %d\n",
		report.GroupBy, report.GeneratedAt.Format(time.RFC3339), report.Status, report.Summary.PreviewReports,
		report.Summary.Customers, totalRosterBreakingRoutes(report.Customers), report.Summary.AddedRoutesOmitted, report.Summary.CustomerEvidenceUnavailable)
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "Note: %s\n", caveat)
	}
	for rank, customer := range report.Customers {
		label := previewSourceDisplay(customer.ID, false)
		if customer.App != "" {
			label += " (app " + previewSourceDisplay(customer.App, false) + ")"
		}
		_, _ = fmt.Fprintf(w, "\n%d. %s — %d breaking / %d affected routes; %d observed requests; last observed %s\n",
			rank+1, label, customer.BreakingRoutes, customer.AffectedRoutes, customer.ObservedRequests, customer.LastObservedAt)
		for _, route := range customer.Routes {
			_, _ = fmt.Fprintf(w, "   %s %s on %s: %s; %d requests; %s", previewSourceDisplay(route.Method, false),
				previewSourceDisplay(route.Path, false), previewSourceDisplay(route.Preview, false),
				previewSourceDisplay(strings.Join(route.Reasons, ", "), false), route.ObservedRequests, previewSourceDisplay(route.LastObservedAt, false))
			if route.Incomplete {
				_, _ = fmt.Fprintf(w, "; incomplete evidence (omitted customer requests: %d)", route.OmittedCustomerRequests)
			}
			_, _ = fmt.Fprintln(w)
		}
	}
}

func renderPreviewCustomerRosterMarkdown(w io.Writer, report previewCustomerRosterReport) {
	_, _ = fmt.Fprintf(w, "## Preview customer impact roster\n\nGrouped by **%s**. Status: **%s**. Reports: %d; customers: %d; added routes omitted: %d; evidence-unavailable reports: %d.\n\n",
		report.GroupBy, report.Status, report.Summary.PreviewReports, report.Summary.Customers, report.Summary.AddedRoutesOmitted, report.Summary.CustomerEvidenceUnavailable)
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "> %s\n\n", caveat)
	}
	_, _ = fmt.Fprintln(w, "| Rank | Customer ID | App scope | Breaking routes | Affected routes | Observed requests | Last observed | Evidence |")
	_, _ = fmt.Fprintln(w, "|---:|---|---|---:|---:|---:|---|---|")
	for rank, customer := range report.Customers {
		_, _ = fmt.Fprintf(w, "| %d | %s | %s | %d | %d | %d | %s | %s |\n", rank+1,
			previewSourceDisplay(customer.ID, true), previewSourceDisplay(customer.App, true), customer.BreakingRoutes, customer.AffectedRoutes,
			customer.ObservedRequests, previewSourceDisplay(customer.LastObservedAt, true), previewSourceDisplay(rosterEvidenceLabel(customer), true))
	}
	for _, customer := range report.Customers {
		_, _ = fmt.Fprintf(w, "\n### %s\n\n", previewSourceDisplay(customer.ID, true))
		for _, route := range customer.Routes {
			_, _ = fmt.Fprintf(w, "- `%s %s` in `%s`: %s; %d observed requests; last seen %s",
				previewSourceDisplay(route.Method, true), previewSourceDisplay(route.Path, true), previewSourceDisplay(route.Preview, true),
				previewSourceDisplay(strings.Join(route.Reasons, ", "), true), route.ObservedRequests, previewSourceDisplay(route.LastObservedAt, true))
			if route.Incomplete {
				_, _ = fmt.Fprintf(w, "; incomplete evidence, %d omitted customer requests", route.OmittedCustomerRequests)
			}
			_, _ = fmt.Fprintln(w, ".")
		}
	}
}

func renderPreviewCustomerRosterCSV(w io.Writer, report previewCustomerRosterReport) error {
	csvWriter := csv.NewWriter(w)
	if err := csvWriter.Write([]string{"rank", "group_by", "customer_id", "app_scope", "preview", "method", "path", "change", "reasons", "breaking", "observed_requests", "last_observed_at", "customer_details_truncated", "omitted_customer_requests", "incomplete"}); err != nil {
		return err
	}
	for rank, customer := range report.Customers {
		for _, route := range customer.Routes {
			row := []string{fmt.Sprint(rank + 1), report.GroupBy, customer.ID, customer.App, route.Preview, route.Method,
				route.Path, route.Change, strings.Join(route.Reasons, ";"), fmt.Sprint(route.Breaking),
				fmt.Sprint(route.ObservedRequests), route.LastObservedAt, fmt.Sprint(route.CustomerDetailsTruncated),
				fmt.Sprint(route.OmittedCustomerRequests), fmt.Sprint(route.Incomplete || customer.Incomplete)}
			for index := range row {
				row[index] = safePreviewCustomerCSVCell(row[index])
			}
			if err := csvWriter.Write(row); err != nil {
				return err
			}
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func safePreviewCustomerCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func rosterEvidenceLabel(customer previewCustomerRosterEntry) string {
	if !customer.Incomplete {
		return "observed"
	}
	return "incomplete: " + strings.Join(customer.IncompleteReasons, ", ")
}

func totalRosterBreakingRoutes(customers []previewCustomerRosterEntry) int {
	total := 0
	for _, customer := range customers {
		total += customer.BreakingRoutes
	}
	return total
}

func laterRFC3339(candidate, current string) bool {
	if candidate == "" {
		return false
	}
	if current == "" {
		return true
	}
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	if candidateErr == nil && currentErr == nil {
		return candidateTime.After(currentTime)
	}
	return candidate > current
}

func laterTimestamp(candidate, current string) string {
	if laterRFC3339(candidate, current) {
		return candidate
	}
	return current
}
