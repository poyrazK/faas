package main

// A read-only, client-side preview of the gateway's edge-rule selectors.
// Explicit client IP and country inputs let it evaluate existing IP and
// geo allow/deny actions without invoking an app or live geo lookup.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const edgeRuleTraceScope = "Host/method/path/header matching is simulated. Per-rule rows show standalone matches against the submitted request; the sequential simulation below composes deterministic actions in gateway phase order, including rewrite and request-header mutations. It stops as incomplete at a matching rule whose result needs runtime state or unavailable request context. A completed 'continue' outcome means the inspected edge-rule phases did not terminate the request, not that the app will return successfully. App-level maintenance, ingress and auth policy, target app existence and ownership, CORS execution, declared routes, body gates, cache, retry, circuit-breaker, async behavior, wake, and backend response are not simulated. IP and geo use supplied --client-ip/--country directly; trusted-proxy validation and live geo lookup are not performed. Equal-priority candidates have no guaranteed order. Results are limited to the named app."

type edgeRuleTraceResult struct {
	App        string                  `json:"app"`
	Host       string                  `json:"host"`
	Path       string                  `json:"path"`
	Method     string                  `json:"method"`
	ClientIP   string                  `json:"client_ip,omitempty"`
	Country    string                  `json:"country,omitempty"`
	Headers    map[string][]string     `json:"headers,omitempty"`
	Scope      string                  `json:"scope"`
	Rules      []edgeRuleTraceRow      `json:"rules"`
	Simulation edgeRuleTraceSimulation `json:"simulation"`
}

type edgeRuleTraceSimulation struct {
	Status            string                        `json:"status"`
	Outcome           string                        `json:"outcome"`
	FinalPath         string                        `json:"final_path"`
	RequestHeaders    map[string][]string           `json:"request_headers,omitempty"`
	ResponseHeaderOps []api.EdgeRuleHeaderOp        `json:"response_header_ops,omitempty"`
	StatusCode        int                           `json:"status_code,omitempty"`
	Location          string                        `json:"location,omitempty"`
	RedirectHeaders   map[string]string             `json:"redirect_headers,omitempty"`
	RetryAfterSeconds int                           `json:"retry_after_seconds,omitempty"`
	Message           string                        `json:"message,omitempty"`
	TargetApp         string                        `json:"target_app,omitempty"`
	Body              json.RawMessage               `json:"body,omitempty"`
	StoppedAt         string                        `json:"stopped_at,omitempty"`
	Reason            string                        `json:"reason"`
	Steps             []edgeRuleTraceSimulationStep `json:"steps"`
}

type edgeRuleTraceSimulationStep struct {
	Phase             string                 `json:"phase"`
	RuleID            string                 `json:"rule_id,omitempty"`
	Kind              string                 `json:"kind,omitempty"`
	Outcome           string                 `json:"outcome"`
	PathBefore        string                 `json:"path_before,omitempty"`
	PathAfter         string                 `json:"path_after,omitempty"`
	StatusCode        int                    `json:"status_code,omitempty"`
	Location          string                 `json:"location,omitempty"`
	RedirectHeaders   map[string]string      `json:"redirect_headers,omitempty"`
	RetryAfterSeconds int                    `json:"retry_after_seconds,omitempty"`
	Message           string                 `json:"message,omitempty"`
	TargetApp         string                 `json:"target_app,omitempty"`
	RequestOps        []api.EdgeRuleHeaderOp `json:"request_header_ops,omitempty"`
	ResponseOps       []api.EdgeRuleHeaderOp `json:"response_header_ops,omitempty"`
	Reason            string                 `json:"reason"`
}

type edgeRuleTraceRow struct {
	ID            string                      `json:"id"`
	Kind          string                      `json:"kind"`
	Priority      int                         `json:"priority"`
	MatchHost     string                      `json:"match_host"`
	MatchPath     string                      `json:"match_path"`
	MatchMethods  []string                    `json:"match_methods"`
	MatchHeaders  map[string]string           `json:"match_headers,omitempty"`
	Status        string                      `json:"status"`
	Reason        string                      `json:"reason"`
	Outcome       string                      `json:"outcome"`
	OutcomeReason string                      `json:"outcome_reason"`
	ActionPreview *edgeRuleTraceActionPreview `json:"action_preview,omitempty"`
	PrecededBy    string                      `json:"preceded_by,omitempty"`
}

// edgeRuleTraceActionPreview reports a single candidate's deterministic
// effect. It intentionally does not combine effects across kinds because
// runtime phase ordering and short-circuit gates are outside this client-side
// preview.
type edgeRuleTraceActionPreview struct {
	Type              string                 `json:"type"`
	TargetApp         string                 `json:"target_app,omitempty"`
	Path              string                 `json:"path,omitempty"`
	StatusCode        int                    `json:"status_code,omitempty"`
	Location          string                 `json:"location,omitempty"`
	RedirectHeaders   map[string]string      `json:"redirect_headers,omitempty"`
	RequestHeaderOps  []api.EdgeRuleHeaderOp `json:"request_header_ops,omitempty"`
	ResponseHeaderOps []api.EdgeRuleHeaderOp `json:"response_header_ops,omitempty"`
	RetryAfterSeconds int                    `json:"retry_after_seconds,omitempty"`
	Message           string                 `json:"message,omitempty"`
	Body              json.RawMessage        `json:"body,omitempty"`
}

func cmdEdgeRulesTrace(args []string) int {
	fs := newFlagSet("edge-rules trace", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	rawURL := fs.String("url", "", "absolute HTTP(S) request URL")
	method := fs.String("method", http.MethodGet, "request method (default GET)")
	clientIPArg := fs.String("client-ip", "", "simulated client IP for kind=ip rules")
	countryArg := fs.String("country", "", "simulated ISO 3166-1 alpha-2 country for kind=geo rules")
	var headerArgs multiFlag
	fs.Var(&headerArgs, "header", "simulated request header (Name:Value; repeat; values compare exactly)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *slug == "" || *rawURL == "" {
		PrintUsage(os.Stderr, "usage: gregale edge-rules trace --app <slug> --url <http(s)://host/path> [--method GET] [--header Name:Value]... [--client-ip IP] [--country CC]", "edge-rules")
		return 1
	}
	u, err := url.Parse(*rawURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return printErr("Invalid --url", fmt.Errorf("expected an absolute HTTP(S) URL without credentials or fragment"))
	}
	requestMethod := strings.ToUpper(*method)
	if _, err := http.NewRequest(requestMethod, u.String(), nil); err != nil || requestMethod == "" {
		return printErr("Invalid --method", fmt.Errorf("expected a valid HTTP method"))
	}
	requestPath := u.Path
	if requestPath == "" {
		requestPath = "/"
	}
	clientIP := ""
	if *clientIPArg != "" {
		parsed := net.ParseIP(*clientIPArg)
		if parsed == nil {
			return printErr("Invalid --client-ip", fmt.Errorf("expected an IPv4 or IPv6 address"))
		}
		clientIP = parsed.String()
	}
	country := strings.ToUpper(strings.TrimSpace(*countryArg))
	if country != "" && !validTraceCountry(country) {
		return printErr("Invalid --country", fmt.Errorf("expected a two-letter ISO country code"))
	}
	requestHeaders, err := parseTraceRequestHeaders(headerArgs)
	if err != nil {
		return printErr("Invalid --header", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rules, err := client.ListEdgeRulesForApp(context.Background(), *slug)
	if err != nil {
		return printErr("List failed", err)
	}
	result := previewEdgeRules(*slug, strings.ToLower(u.Hostname()), requestPath, requestMethod, clientIP, country, rules, requestHeaders)
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "%s %s%s (app %s)\n", result.Method, result.Host, result.Path, result.App)
	if result.ClientIP != "" || result.Country != "" {
		_, _ = fmt.Fprintf(osStdout, "simulated context: client_ip=%s country=%s\n", emptyAsDash(result.ClientIP), emptyAsDash(result.Country))
	}
	headerNames := make([]string, 0, len(result.Headers))
	for name := range result.Headers {
		headerNames = append(headerNames, name)
	}
	sort.Strings(headerNames)
	for _, name := range headerNames {
		values := result.Headers[name]
		for _, value := range values {
			_, _ = fmt.Fprintf(osStdout, "header: %s=%q\n", name, value)
		}
	}
	if len(result.Rules) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no edge rules for this app)")
	} else {
		for _, row := range result.Rules {
			_, _ = fmt.Fprintf(osStdout, "%-18s %-16s outcome=%-16s priority=%-5d %s — %s; %s\n", row.Kind, row.Status, row.Outcome, row.Priority, row.ID, row.Reason, row.OutcomeReason)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "simulation: status=%s outcome=%s final_path=%s\n", result.Simulation.Status, result.Simulation.Outcome, result.Simulation.FinalPath)
	for _, step := range result.Simulation.Steps {
		_, _ = fmt.Fprintf(osStdout, "  %-12s %-12s %s — %s\n", step.Phase, step.Outcome, step.RuleID, step.Reason)
	}
	if result.Simulation.StatusCode != 0 {
		_, _ = fmt.Fprintf(osStdout, "  response: status=%d", result.Simulation.StatusCode)
		if result.Simulation.Location != "" {
			_, _ = fmt.Fprintf(osStdout, " location=%q", result.Simulation.Location)
		}
		if result.Simulation.RetryAfterSeconds != 0 {
			_, _ = fmt.Fprintf(osStdout, " retry_after=%d", result.Simulation.RetryAfterSeconds)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	if result.Simulation.Message != "" {
		_, _ = fmt.Fprintf(osStdout, "  response message: %q\n", result.Simulation.Message)
	}
	if len(result.Simulation.RedirectHeaders) > 0 {
		names := make([]string, 0, len(result.Simulation.RedirectHeaders))
		for name := range result.Simulation.RedirectHeaders {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			_, _ = fmt.Fprintf(osStdout, "  redirect header: %s=%q\n", name, result.Simulation.RedirectHeaders[name])
		}
	}
	if result.Simulation.TargetApp != "" {
		_, _ = fmt.Fprintf(osStdout, "  target app: %s\n", result.Simulation.TargetApp)
	}
	for _, op := range result.Simulation.ResponseHeaderOps {
		_, _ = fmt.Fprintf(osStdout, "  response header op: %s %s=%q\n", op.Action, op.Name, op.Value)
	}
	finalHeaderNames := make([]string, 0, len(result.Simulation.RequestHeaders))
	for name := range result.Simulation.RequestHeaders {
		finalHeaderNames = append(finalHeaderNames, name)
	}
	sort.Strings(finalHeaderNames)
	for _, name := range finalHeaderNames {
		for _, value := range result.Simulation.RequestHeaders[name] {
			_, _ = fmt.Fprintf(osStdout, "  simulated request header: %s=%q\n", name, value)
		}
	}
	if result.Simulation.StoppedAt != "" {
		_, _ = fmt.Fprintf(osStdout, "  simulation stopped at %s: %s\n", result.Simulation.StoppedAt, result.Simulation.Reason)
	}
	_, _ = fmt.Fprintln(osStdout, result.Scope)
	return 0
}

// previewEdgeRules mirrors the store's enabled host selection and the
// gateway's path.Match / method filters. The app-list API sorts equal
// priorities newest-first, while the gateway host read sorts oldest-first;
// re-sort before identifying the first candidate per kind.
func previewEdgeRules(app, host, requestPath, method, clientIP, country string, rules []api.EdgeRuleResponse, suppliedHeaders ...http.Header) edgeRuleTraceResult {
	var requestHeaders http.Header
	if len(suppliedHeaders) > 0 {
		requestHeaders = suppliedHeaders[0]
	}
	sorted := append([]api.EdgeRuleResponse(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})
	result := edgeRuleTraceResult{App: app, Host: host, Path: requestPath, Method: method, ClientIP: clientIP, Country: country, Headers: traceHeaderSnapshot(requestHeaders), Scope: edgeRuleTraceScope, Rules: make([]edgeRuleTraceRow, 0, len(sorted))}
	firstByKind := make(map[string]int)
	for _, rule := range sorted {
		row := edgeRuleTraceRow{
			ID: rule.ID, Kind: rule.Kind, Priority: rule.Priority,
			MatchHost: rule.MatchHost, MatchPath: rule.MatchPath, MatchMethods: rule.MatchMethods, MatchHeaders: rule.MatchHeaders,
		}
		switch {
		case !rule.Enabled:
			row.Status, row.Reason = "skipped", "rule is disabled"
		case !traceHostMatches(rule.MatchHost, host):
			row.Status, row.Reason = "skipped", fmt.Sprintf("host %q does not match %q", host, rule.MatchHost)
		case !traceMethodMatches(rule.MatchMethods, method):
			row.Status, row.Reason = "skipped", fmt.Sprintf("method %q is not in %s", method, strings.Join(rule.MatchMethods, ", "))
		case !api.EdgeRuleRequestHeadersMatch(rule.MatchHeaders, requestHeaders):
			row.Status, row.Reason = "skipped", traceHeaderMismatch(rule.MatchHeaders, requestHeaders)
		default:
			matched := true
			var err error
			if rule.MatchPath != "" && rule.MatchPath != "*" {
				matched, err = path.Match(rule.MatchPath, requestPath)
			}
			switch {
			case err != nil:
				row.Status, row.Reason = "skipped", fmt.Sprintf("invalid path glob %q: %v", rule.MatchPath, err)
			case !matched:
				row.Status, row.Reason = "skipped", fmt.Sprintf("path %q does not match %q", requestPath, rule.MatchPath)
			default:
				if firstIndex, seen := firstByKind[rule.Kind]; seen {
					first := &result.Rules[firstIndex]
					if first.Priority == rule.Priority {
						if first.Status != "tied_candidate" {
							first.Status = "tied_candidate"
							first.Reason = strings.TrimSuffix(first.Reason, "; first candidate of this kind") + "; equal-priority candidates have no guaranteed order"
						}
						row.Status, row.Reason = "tied_candidate", traceMatchedSelectors(rule)+"; equal-priority candidates have no guaranteed order"
					} else {
						row.Status, row.PrecededBy = "later_candidate", first.ID
						row.Reason = fmt.Sprintf("%s; higher-priority %s candidate %s has precedence", traceMatchedSelectors(rule), rule.Kind, row.PrecededBy)
					}
				} else {
					row.Status, row.Reason = "first_candidate", traceMatchedSelectors(rule)+"; first candidate of this kind"
					firstByKind[rule.Kind] = len(result.Rules)
				}
			}
		}
		row.Outcome, row.OutcomeReason, row.ActionPreview = previewEdgeRuleOutcome(rule, row, clientIP, country, requestPath)
		result.Rules = append(result.Rules, row)
	}
	result.Simulation = simulateEdgeRuleRequest(host, requestPath, method, clientIP, country, sorted, requestHeaders)
	return result
}

// simulateEdgeRuleRequest composes the edge-rule phases whose effects can be
// represented without running gateway dependencies. The phase sequence is
// pinned to Handler.ServeHTTP: route substitution precedes the app handler;
// maintenance, redirect, rewrite, and headers run before CORS/auth/IP/geo and
// later body gates; a fixed response is considered after those gates. If a
// matched phase needs runtime state, the trace stops there rather than
// assuming it passes.
func simulateEdgeRuleRequest(host, requestPath, method, clientIP, country string, rules []api.EdgeRuleResponse, requestHeaders http.Header) edgeRuleTraceSimulation {
	simulation := edgeRuleTraceSimulation{
		Status: "complete", Outcome: "continue", FinalPath: requestPath,
		RequestHeaders: traceHeaderSnapshot(cloneTraceHeaders(requestHeaders)),
		Reason:         "no simulated edge-rule action terminated the request; downstream app and gateway behavior is outside this simulation",
		Steps:          make([]edgeRuleTraceSimulationStep, 0, 6),
	}
	workingHeaders := cloneTraceHeaders(requestHeaders)
	stop := func(status, outcome, phase, reason string, rule *api.EdgeRuleResponse) edgeRuleTraceSimulation {
		simulation.Status = status
		simulation.Outcome = outcome
		simulation.StoppedAt = phase
		simulation.Reason = reason
		step := edgeRuleTraceSimulationStep{Phase: phase, Outcome: outcome, Reason: reason}
		if rule != nil {
			step.RuleID, step.Kind = rule.ID, rule.Kind
		}
		simulation.Steps = append(simulation.Steps, step)
		simulation.FinalPath = requestPath
		simulation.RequestHeaders = traceHeaderSnapshot(workingHeaders)
		return simulation
	}

	phases := []string{"route", "maintenance", "redirect", "rewrite", "headers", "cors", "jwt", "ip", "geo", "limit", "throttle", "validate", "respond"}
	for _, phase := range phases {
		rule, tied := firstTracePhaseRule(rules, phase, host, requestPath, method, workingHeaders)
		if tied {
			return stop("incomplete", "ambiguous", phase, "equal-priority matching rules have no guaranteed evaluation order", rule)
		}
		if rule == nil {
			continue
		}
		row := edgeRuleTraceRow{Status: "first_candidate"}
		outcome, reason, preview := previewEdgeRuleOutcome(*rule, row, clientIP, country, requestPath)
		step := edgeRuleTraceSimulationStep{Phase: phase, RuleID: rule.ID, Kind: phase, Outcome: outcome, PathBefore: requestPath, Reason: reason}
		if preview != nil {
			step.StatusCode, step.Location, step.TargetApp = preview.StatusCode, preview.Location, preview.TargetApp
			step.RedirectHeaders = cloneTraceStringMap(preview.RedirectHeaders)
			step.RetryAfterSeconds, step.Message = preview.RetryAfterSeconds, preview.Message
			step.RequestOps = append([]api.EdgeRuleHeaderOp(nil), preview.RequestHeaderOps...)
			step.ResponseOps = append([]api.EdgeRuleHeaderOp(nil), preview.ResponseHeaderOps...)
		}

		switch phase {
		case "route":
			if outcome != "route" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "incomplete", "route"
			simulation.TargetApp, simulation.StoppedAt = preview.TargetApp, phase
			simulation.Reason = "request would be routed to another app; that app's rules are not loaded by this app-scoped trace"
			step.Reason = simulation.Reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath = requestPath
			simulation.RequestHeaders = traceHeaderSnapshot(workingHeaders)
			return simulation
		case "maintenance":
			if outcome != "maintenance" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome, simulation.StatusCode = "complete", "maintenance", preview.StatusCode
			simulation.RetryAfterSeconds, simulation.Message = preview.RetryAfterSeconds, preview.Message
			simulation.StoppedAt, simulation.Reason = phase, reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, traceHeaderSnapshot(workingHeaders)
			return simulation
		case "redirect":
			if outcome != "redirect" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "complete", "redirect"
			simulation.StatusCode, simulation.Location = preview.StatusCode, preview.Location
			simulation.RedirectHeaders = cloneTraceStringMap(preview.RedirectHeaders)
			simulation.StoppedAt, simulation.Reason = phase, reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, traceHeaderSnapshot(workingHeaders)
			return simulation
		case "rewrite":
			if outcome != "rewrite" && outcome != "not_applied" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			if outcome == "rewrite" {
				requestPath = preview.Path
			}
			step.PathAfter = requestPath
			simulation.Steps = append(simulation.Steps, step)
		case "headers":
			if outcome != "headers" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			applyTraceHeaderOps(workingHeaders, preview.RequestHeaderOps)
			simulation.ResponseHeaderOps = append(simulation.ResponseHeaderOps, preview.ResponseHeaderOps...)
			step.PathAfter = requestPath
			simulation.Steps = append(simulation.Steps, step)
		case "ip", "geo":
			switch outcome {
			case "allow":
				simulation.Steps = append(simulation.Steps, step)
			case "block":
				simulation.Status, simulation.Outcome, simulation.StatusCode = "complete", "blocked", http.StatusForbidden
				simulation.StoppedAt, simulation.Reason = phase, reason
				step.StatusCode = http.StatusForbidden
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, traceHeaderSnapshot(workingHeaders)
				return simulation
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		case "respond":
			if outcome != "fixed_response" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "incomplete", "fixed_response"
			simulation.StatusCode, simulation.Body = preview.StatusCode, append(json.RawMessage(nil), preview.Body...)
			simulation.StoppedAt, simulation.Reason = phase, "edge rule would return this fixed response if prior app authentication and runtime gates pass"
			step.Reason = simulation.Reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, traceHeaderSnapshot(workingHeaders)
			return simulation
		default:
			// CORS, JWT, and the body-dependent gates may short-circuit based
			// on runtime state that this CLI does not collect.
			return stop("incomplete", "needs_runtime_context", phase, "matching rule depends on gateway runtime state or request data that this trace does not collect", rule)
		}
	}
	simulation.FinalPath = requestPath
	simulation.RequestHeaders = traceHeaderSnapshot(workingHeaders)
	return simulation
}

func firstTracePhaseRule(rules []api.EdgeRuleResponse, kind, host, requestPath, method string, headers http.Header) (*api.EdgeRuleResponse, bool) {
	var first *api.EdgeRuleResponse
	for i := range rules {
		rule := &rules[i]
		if rule.Kind != kind || !rule.Enabled || !traceHostMatches(rule.MatchHost, host) || !traceMethodMatches(rule.MatchMethods, method) || !api.EdgeRuleRequestHeadersMatch(rule.MatchHeaders, headers) {
			continue
		}
		matched, err := true, error(nil)
		if rule.MatchPath != "" && rule.MatchPath != "*" {
			matched, err = path.Match(rule.MatchPath, requestPath)
		}
		if err != nil || !matched {
			continue
		}
		if first == nil {
			first = rule
			continue
		}
		return first, first.Priority == rule.Priority
	}
	return first, false
}

func cloneTraceHeaders(headers http.Header) http.Header {
	cloned := make(http.Header, len(headers))
	for name, values := range headers {
		cloned[name] = append([]string(nil), values...)
	}
	return cloned
}

func cloneTraceStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func applyTraceHeaderOps(headers http.Header, ops []api.EdgeRuleHeaderOp) {
	for _, op := range ops {
		switch op.Action {
		case "remove":
			headers.Del(op.Name)
		case "set":
			if op.Value == "" {
				headers.Del(op.Name)
			} else {
				headers.Set(op.Name, op.Value)
			}
		case "add":
			if op.Value != "" {
				headers.Add(op.Name, op.Value)
			}
		}
	}
}

func previewEdgeRuleOutcome(rule api.EdgeRuleResponse, row edgeRuleTraceRow, clientIP, country, requestPath string) (string, string, *edgeRuleTraceActionPreview) {
	switch row.Status {
	case "skipped":
		return "not_applicable", "static selectors did not match", nil
	case "later_candidate":
		return "not_evaluated", "a higher-priority matching candidate is considered first", nil
	case "tied_candidate":
		return "ambiguous", "equal-priority rules have no guaranteed evaluation order", nil
	case "first_candidate":
	default:
		return "unknown", "unrecognized trace status", nil
	}

	switch rule.Kind {
	case "route":
		action, ok := decodeEdgeRuleTraceAction[api.EdgeRuleRouteAction](rule.Action, "route")
		if !ok || action.TargetAppSlug == "" {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &edgeRuleTraceActionPreview{Type: "route", TargetApp: action.TargetAppSlug}
		return "route", fmt.Sprintf("would route to app %q", action.TargetAppSlug), preview
	case "rewrite":
		action, ok := decodeEdgeRuleTraceAction[api.EdgeRuleRewriteAction](rule.Action, "rewrite")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &edgeRuleTraceActionPreview{Type: "rewrite", Path: requestPath}
		rewritten, applies := traceRewritePath(requestPath, *action)
		if !applies {
			return "not_applied", fmt.Sprintf("rewrite prefix %q does not match request path %q", action.From, requestPath), preview
		}
		preview.Path = rewritten
		if rewritten == requestPath {
			return "rewrite", fmt.Sprintf("rewrite leaves request path at %q", rewritten), preview
		}
		return "rewrite", fmt.Sprintf("would rewrite request path to %q", rewritten), preview
	case "redirect":
		action, ok := decodeEdgeRuleTraceAction[api.EdgeRuleRedirectAction](rule.Action, "redirect")
		if !ok || action.To == "" {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		status := action.StatusCode
		switch status {
		case 301, 302, 307, 308:
		default:
			status = http.StatusFound
		}
		preview := &edgeRuleTraceActionPreview{Type: "redirect", StatusCode: status, Location: action.To, RedirectHeaders: action.Headers}
		return "redirect", fmt.Sprintf("would return HTTP %d redirect to %q", status, action.To), preview
	case "headers":
		action, ok := decodeEdgeRuleTraceAction[api.EdgeRuleHeadersAction](rule.Action, "headers")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &edgeRuleTraceActionPreview{
			Type:              "headers",
			RequestHeaderOps:  append([]api.EdgeRuleHeaderOp(nil), action.RequestHeaders...),
			ResponseHeaderOps: append([]api.EdgeRuleHeaderOp(nil), action.ResponseHeaders...),
		}
		return "headers", fmt.Sprintf("would apply %d request-header and %d response-header operation(s)", len(action.RequestHeaders), len(action.ResponseHeaders)), preview
	case "maintenance":
		action, ok := decodeEdgeRuleTraceAction[api.EdgeRuleMaintenanceAction](rule.Action, "maintenance")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		retryAfter := action.RetryAfterSeconds
		if retryAfter <= 0 {
			retryAfter = api.EdgeRuleMaintenanceRetryAfterSeconds
		}
		preview := &edgeRuleTraceActionPreview{Type: "maintenance", StatusCode: http.StatusServiceUnavailable, RetryAfterSeconds: retryAfter, Message: action.Message}
		return "maintenance", fmt.Sprintf("would return HTTP 503 maintenance response (Retry-After %d)", retryAfter), preview
	case "respond":
		action, ok := decodeEdgeRuleTraceAction[api.EdgeRuleRespondAction](rule.Action, "respond")
		if !ok || action.StatusCode < http.StatusOK || action.StatusCode > 599 {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		body := append(json.RawMessage(nil), action.Body...)
		preview := &edgeRuleTraceActionPreview{Type: "respond", StatusCode: action.StatusCode, Body: body}
		return "fixed_response", fmt.Sprintf("would return fixed HTTP %d response", action.StatusCode), preview
	case "ip":
		if clientIP == "" {
			return "needs_context", "supply --client-ip to evaluate this rule", nil
		}
		var envelope struct {
			IP *api.EdgeRuleIPAction `json:"ip"`
		}
		if err := json.Unmarshal(rule.Action, &envelope); err != nil || envelope.IP == nil {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		outcome, reason := evaluateIPTrace(*envelope.IP, net.ParseIP(clientIP))
		return outcome, reason, nil
	case "geo":
		if country == "" {
			return "needs_context", "supply --country; trace does not consult the live geo database", nil
		}
		var envelope struct {
			Geo *api.EdgeRuleGeoAction `json:"geo"`
		}
		if err := json.Unmarshal(rule.Action, &envelope); err != nil || envelope.Geo == nil {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		outcome, reason := evaluateGeoTrace(*envelope.Geo, country)
		return outcome, reason, nil
	default:
		return "not_simulated", "this rule kind has runtime behavior outside the action preview", nil
	}
}

func decodeEdgeRuleTraceAction[T any](raw json.RawMessage, kind string) (*T, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	encoded, ok := envelope[kind]
	if !ok {
		return nil, false
	}
	var action T
	if err := json.Unmarshal(encoded, &action); err != nil {
		return nil, false
	}
	return &action, true
}

// traceRewritePath mirrors the gateway's prefix-strip + replacement rules.
// It returns false when the selectors matched but the configured From prefix
// does not actually match the request path, which the gateway treats as a miss.
func traceRewritePath(requestPath string, action api.EdgeRuleRewriteAction) (string, bool) {
	return api.ApplyEdgeRuleRewritePath(requestPath, action.From, action.To)
}

func evaluateIPTrace(action api.EdgeRuleIPAction, clientIP net.IP) (string, string) {
	deny, ok := parseTraceCIDRs(action.Deny)
	if !ok {
		return "unavailable", "rule contains an invalid deny CIDR; gateway compilation would drop it"
	}
	allow, ok := parseTraceCIDRs(action.Allow)
	if !ok {
		return "unavailable", "rule contains an invalid allow CIDR; gateway compilation would drop it"
	}
	for i, network := range deny {
		if network.Contains(clientIP) {
			return "block", fmt.Sprintf("client IP matches deny CIDR %q", action.Deny[i])
		}
	}
	if len(allow) > 0 {
		for i, network := range allow {
			if network.Contains(clientIP) {
				return "allow", fmt.Sprintf("client IP matches allow CIDR %q and no deny CIDR matched", action.Allow[i])
			}
		}
		return "block", "client IP matched no allow CIDR (implicit deny)"
	}
	return "allow", "no deny CIDR matched and the rule has no allowlist"
}

func parseTraceCIDRs(cidrs []string) ([]*net.IPNet, bool) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, false
		}
		nets = append(nets, network)
	}
	return nets, true
}

func evaluateGeoTrace(action api.EdgeRuleGeoAction, country string) (string, string) {
	for _, denied := range action.Deny {
		if strings.EqualFold(denied, country) {
			return "block", fmt.Sprintf("country %s matches the deny list", country)
		}
	}
	if len(action.Allow) > 0 {
		for _, allowed := range action.Allow {
			if strings.EqualFold(allowed, country) {
				return "allow", fmt.Sprintf("country %s matches the allow list and no deny code matched", country)
			}
		}
		return "block", fmt.Sprintf("country %s matches no allow code (implicit deny)", country)
	}
	return "allow", fmt.Sprintf("country %s matches no deny code and the rule has no allowlist", country)
}

func validTraceCountry(country string) bool {
	if len(country) != 2 {
		return false
	}
	for i := 0; i < len(country); i++ {
		if country[i] < 'A' || country[i] > 'Z' {
			return false
		}
	}
	return true
}

func emptyAsDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func traceMatchedSelectors(rule api.EdgeRuleResponse) string {
	methods := strings.Join(rule.MatchMethods, ",")
	if methods == "" {
		methods = "*"
	}
	matchPath := rule.MatchPath
	if matchPath == "" {
		matchPath = "*"
	}
	selectors := fmt.Sprintf("host %q, method %q, path %q", rule.MatchHost, methods, matchPath)
	if len(rule.MatchHeaders) > 0 {
		headerNames := make([]string, 0, len(rule.MatchHeaders))
		for name := range rule.MatchHeaders {
			headerNames = append(headerNames, name)
		}
		sort.Strings(headerNames)
		var conditions []string
		for _, name := range headerNames {
			conditions = append(conditions, fmt.Sprintf("%s=%q", name, rule.MatchHeaders[name]))
		}
		selectors += ", headers [" + strings.Join(conditions, ", ") + "]"
	}
	return selectors + " match"
}

func parseTraceRequestHeaders(items []string) (http.Header, error) {
	headers := make(http.Header)
	for _, raw := range items {
		index := strings.IndexByte(raw, ':')
		if index < 1 {
			return nil, fmt.Errorf("%q: expected Name:Value", raw)
		}
		name, value := raw[:index], raw[index+1:]
		normalized, err := api.NormalizeEdgeRuleMatchHeaders(map[string]string{name: value})
		if err != nil {
			return nil, err
		}
		for headerName := range normalized {
			headers.Add(headerName, value)
		}
	}
	return headers, nil
}

func traceHeaderSnapshot(headers http.Header) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]string, len(headers))
	for name, values := range headers {
		key := strings.ToLower(name)
		out[key] = append(out[key], values...)
	}
	return out
}

func traceHeaderMismatch(expected map[string]string, actual http.Header) string {
	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := expected[name]
		if !api.EdgeRuleRequestHeadersMatch(map[string]string{name: value}, actual) {
			return fmt.Sprintf("request header %q has no value equal to %q", name, value)
		}
	}
	return "request headers do not match"
}

// Store.MatchEdgeRulesForHost accepts only exact hosts, '*', or a
// '*.<suffix>' subdomain pattern; patterns are lowercased on write.
func traceHostMatches(pattern, host string) bool {
	if pattern == "*" || pattern == host {
		return true
	}
	return strings.HasPrefix(pattern, "*.") && len(host) > len(pattern)-1 && strings.HasSuffix(host, pattern[1:])
}

func traceMethodMatches(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, candidate := range methods {
		if strings.ToUpper(candidate) == method {
			return true
		}
	}
	return false
}
