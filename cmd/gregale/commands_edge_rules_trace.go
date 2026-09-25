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

const edgeRuleTraceScope = "Host/method/path/header matching is simulated. Header names are case-insensitive and values compare exactly; repeated request-header values are preserved. Per-rule previews cover route, rewrite, redirect, header operations, maintenance, fixed responses, and IP/geo decisions. Previews are not combined into a final gateway response: runtime phase ordering, cross-kind short-circuits, auth, stateful gates, CORS, validation, cache, retry, circuit-breaker, and async behavior are not simulated. IP and geo use supplied --client-ip/--country directly; trusted-proxy validation and live geo lookup are not performed. Equal-priority candidates have no guaranteed order. Results are limited to the named app."

type edgeRuleTraceResult struct {
	App      string              `json:"app"`
	Host     string              `json:"host"`
	Path     string              `json:"path"`
	Method   string              `json:"method"`
	ClientIP string              `json:"client_ip,omitempty"`
	Country  string              `json:"country,omitempty"`
	Headers  map[string][]string `json:"headers,omitempty"`
	Scope    string              `json:"scope"`
	Rules    []edgeRuleTraceRow  `json:"rules"`
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
	return result
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
