// Package edgeruletrace contains the deterministic, read-only edge-rule
// request simulator shared by gregale and the dashboard.
package edgeruletrace

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

const (
	// MaxTraceBodyBytes bounds request payloads accepted by the CLI and
	// dashboard simulator. It is intentionally lower than gateway limits so a
	// trace cannot consume unbounded memory or schema-validation time.
	MaxTraceBodyBytes = 1 << 20

	Scope = "Host/method/path/header matching is simulated. Per-rule rows show standalone matches against the submitted request; the sequential simulation composes deterministic actions in gateway phase order. A supplied body is limited to 1 MiB and is evaluated only for validate rules; its contents are never included in the result. The trace stops as incomplete where runtime state or unavailable request context is required. A completed 'continue' outcome means inspected edge-rule phases did not terminate the request, not that the app will return successfully. App-level maintenance, ingress/auth policy, target-app rules after routing, CORS execution, declared routes, explicit limit rules, throttle state, cache, retry, circuit-breaker, async behavior, wake, and backend response are not simulated. IP and geo use supplied client_ip/country directly; trusted-proxy validation and live geo lookup are not performed. Equal-priority candidates have no guaranteed order. Results are limited to the named app."
)

// Input is the request context that can be simulated without contacting the
// gateway or app runtime. Call NormalizeInput before Simulate.
type Input struct {
	App      string
	Host     string
	Path     string
	Method   string
	ClientIP string
	Country  string
	Headers  http.Header
	Body     []byte
	// BodyProvided distinguishes an intentionally empty body from omitted
	// request-body context. Validate rules remain incomplete when omitted.
	BodyProvided bool
	// RequestBodyMaxBytes is the app's effective plan cap. Zero selects the
	// platform maximum for callers that do not have app metadata.
	RequestBodyMaxBytes int64
}

type Result struct {
	App          string              `json:"app"`
	Host         string              `json:"host"`
	Path         string              `json:"path"`
	Method       string              `json:"method"`
	ClientIP     string              `json:"client_ip,omitempty"`
	Country      string              `json:"country,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	BodyProvided bool                `json:"body_provided"`
	BodyBytes    int                 `json:"body_bytes,omitempty"`
	Scope        string              `json:"scope"`
	Rules        []RuleRow           `json:"rules"`
	Simulation   Simulation          `json:"simulation"`
}

type Simulation struct {
	Status            string                 `json:"status"`
	Outcome           string                 `json:"outcome"`
	FinalPath         string                 `json:"final_path"`
	RequestHeaders    map[string][]string    `json:"request_headers,omitempty"`
	ResponseHeaderOps []api.EdgeRuleHeaderOp `json:"response_header_ops,omitempty"`
	StatusCode        int                    `json:"status_code,omitempty"`
	Location          string                 `json:"location,omitempty"`
	RedirectHeaders   map[string]string      `json:"redirect_headers,omitempty"`
	RetryAfterSeconds int                    `json:"retry_after_seconds,omitempty"`
	Message           string                 `json:"message,omitempty"`
	TargetApp         string                 `json:"target_app,omitempty"`
	Body              json.RawMessage        `json:"body,omitempty"`
	StoppedAt         string                 `json:"stopped_at,omitempty"`
	Reason            string                 `json:"reason"`
	Steps             []SimulationStep       `json:"steps"`
}

type SimulationStep struct {
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
	ValidationField   string                 `json:"validation_field,omitempty"`
	ValidationKeyword string                 `json:"validation_keyword,omitempty"`
	RequestOps        []api.EdgeRuleHeaderOp `json:"request_header_ops,omitempty"`
	ResponseOps       []api.EdgeRuleHeaderOp `json:"response_header_ops,omitempty"`
	Reason            string                 `json:"reason"`
}

type RuleRow struct {
	ID            string            `json:"id"`
	Kind          string            `json:"kind"`
	Priority      int               `json:"priority"`
	MatchHost     string            `json:"match_host"`
	MatchPath     string            `json:"match_path"`
	MatchMethods  []string          `json:"match_methods"`
	MatchHeaders  map[string]string `json:"match_headers,omitempty"`
	Status        string            `json:"status"`
	Reason        string            `json:"reason"`
	Outcome       string            `json:"outcome"`
	OutcomeReason string            `json:"outcome_reason"`
	ActionPreview *ActionPreview    `json:"action_preview,omitempty"`
	PrecededBy    string            `json:"preceded_by,omitempty"`
}

type ActionPreview struct {
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
	ValidationField   string                 `json:"validation_field,omitempty"`
	ValidationKeyword string                 `json:"validation_keyword,omitempty"`
	Body              json.RawMessage        `json:"body,omitempty"`
}

// NormalizeInput validates user-supplied request context and canonicalizes
// only the same fields the CLI has historically normalized (method, host,
// client IP, and country). Header value comparisons remain exact.
func NormalizeInput(input Input) (Input, error) {
	input.App = strings.TrimSpace(input.App)
	if input.App == "" || len(input.App) > 40 {
		return Input{}, fmt.Errorf("app slug is required and must be at most 40 characters")
	}
	input.Host = strings.ToLower(strings.TrimSpace(input.Host))
	if !validRequestHost(input.Host) {
		return Input{}, fmt.Errorf("host must be a hostname or IP address without a port")
	}
	if input.Path == "" {
		input.Path = "/"
	}
	if len(input.Body) > MaxTraceBodyBytes {
		return Input{}, fmt.Errorf("request body must not exceed %d bytes", MaxTraceBodyBytes)
	}
	if len(input.Body) > 0 {
		input.BodyProvided = true
	}
	if input.RequestBodyMaxBytes <= 0 || input.RequestBodyMaxBytes > api.MaxRequestBodyBytes {
		input.RequestBodyMaxBytes = api.MaxRequestBodyBytes
	}
	if !strings.HasPrefix(input.Path, "/") || len(input.Path) > 2048 {
		return Input{}, fmt.Errorf("path must start with / and be at most 2048 characters")
	}
	input.Method = strings.ToUpper(strings.TrimSpace(input.Method))
	if input.Method == "" {
		input.Method = http.MethodGet
	}
	if _, err := http.NewRequest(input.Method, "http://trace.invalid/", nil); err != nil {
		return Input{}, fmt.Errorf("invalid HTTP method")
	}
	if input.ClientIP != "" {
		ip := net.ParseIP(strings.TrimSpace(input.ClientIP))
		if ip == nil {
			return Input{}, fmt.Errorf("client IP must be an IPv4 or IPv6 address")
		}
		input.ClientIP = ip.String()
	}
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	if input.Country != "" && !validCountry(input.Country) {
		return Input{}, fmt.Errorf("country must be a two-letter ISO country code")
	}
	headers, err := normalizeRequestHeaders(input.Headers)
	if err != nil {
		return Input{}, err
	}
	input.Headers = headers
	return input, nil
}

// ParseRequestHeaders parses repeated Name:Value inputs. It retains repeated
// values and compares their bytes exactly, as edge-rule selectors do.
func ParseRequestHeaders(items []string) (http.Header, error) {
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

// Simulate evaluates the named app's edge rules without side effects.
func Simulate(input Input, rules []api.EdgeRuleResponse) (Result, error) {
	normalized, err := NormalizeInput(input)
	if err != nil {
		return Result{}, err
	}
	return previewNormalized(normalized, rules), nil
}

func previewNormalized(input Input, rules []api.EdgeRuleResponse) Result {
	sorted := append([]api.EdgeRuleResponse(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})
	result := Result{
		App: input.App, Host: input.Host, Path: input.Path, Method: input.Method,
		ClientIP: input.ClientIP, Country: input.Country,
		BodyProvided: input.BodyProvided, BodyBytes: len(input.Body),
		Headers: headerSnapshot(input.Headers), Scope: Scope,
		Rules: make([]RuleRow, 0, len(sorted)),
	}
	firstByKind := make(map[string]int)
	for _, rule := range sorted {
		row := RuleRow{
			ID: rule.ID, Kind: rule.Kind, Priority: rule.Priority,
			MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
			MatchMethods: rule.MatchMethods, MatchHeaders: rule.MatchHeaders,
		}
		switch {
		case !rule.Enabled:
			row.Status, row.Reason = "skipped", "rule is disabled"
		case !HostMatches(rule.MatchHost, input.Host):
			row.Status, row.Reason = "skipped", fmt.Sprintf("host %q does not match %q", input.Host, rule.MatchHost)
		case !MethodMatches(rule.MatchMethods, input.Method):
			row.Status, row.Reason = "skipped", fmt.Sprintf("method %q is not in %s", input.Method, strings.Join(rule.MatchMethods, ", "))
		case !api.EdgeRuleRequestHeadersMatch(rule.MatchHeaders, input.Headers):
			row.Status, row.Reason = "skipped", headerMismatch(rule.MatchHeaders, input.Headers)
		default:
			matched, matchErr := true, error(nil)
			if rule.MatchPath != "" && rule.MatchPath != "*" {
				matched, matchErr = path.Match(rule.MatchPath, input.Path)
			}
			switch {
			case matchErr != nil:
				row.Status, row.Reason = "skipped", fmt.Sprintf("invalid path glob %q: %v", rule.MatchPath, matchErr)
			case !matched:
				row.Status, row.Reason = "skipped", fmt.Sprintf("path %q does not match %q", input.Path, rule.MatchPath)
			default:
				if firstIndex, seen := firstByKind[rule.Kind]; seen {
					first := &result.Rules[firstIndex]
					if first.Priority == rule.Priority {
						if first.Status != "tied_candidate" {
							first.Status = "tied_candidate"
							first.Reason = strings.TrimSuffix(first.Reason, "; first candidate of this kind") + "; equal-priority candidates have no guaranteed order"
						}
						row.Status, row.Reason = "tied_candidate", matchedSelectors(rule)+"; equal-priority candidates have no guaranteed order"
					} else {
						row.Status, row.PrecededBy = "later_candidate", first.ID
						row.Reason = fmt.Sprintf("%s; higher-priority %s candidate %s has precedence", matchedSelectors(rule), rule.Kind, row.PrecededBy)
					}
				} else {
					row.Status, row.Reason = "first_candidate", matchedSelectors(rule)+"; first candidate of this kind"
					firstByKind[rule.Kind] = len(result.Rules)
				}
			}
		}
		row.Outcome, row.OutcomeReason, row.ActionPreview = previewAction(rule, row, input, input.Path)
		result.Rules = append(result.Rules, row)
	}
	result.Simulation = simulateRequest(input, sorted)
	return result
}

func simulateRequest(input Input, rules []api.EdgeRuleResponse) Simulation {
	simulation := Simulation{
		Status: "complete", Outcome: "continue", FinalPath: input.Path,
		RequestHeaders: headerSnapshot(cloneHeaders(input.Headers)),
		Reason:         "no simulated edge-rule action terminated the request; downstream app and gateway behavior is outside this simulation",
		Steps:          make([]SimulationStep, 0, 6),
	}
	workingHeaders := cloneHeaders(input.Headers)
	requestPath := input.Path
	stop := func(status, outcome, phase, reason string, rule *api.EdgeRuleResponse) Simulation {
		simulation.Status = status
		simulation.Outcome = outcome
		simulation.StoppedAt = phase
		simulation.Reason = reason
		step := SimulationStep{Phase: phase, Outcome: outcome, Reason: reason}
		if rule != nil {
			step.RuleID, step.Kind = rule.ID, rule.Kind
		}
		simulation.Steps = append(simulation.Steps, step)
		simulation.FinalPath = requestPath
		simulation.RequestHeaders = headerSnapshot(workingHeaders)
		return simulation
	}

	phases := []string{"route", "maintenance", "redirect", "rewrite", "headers", "cors", "jwt", "ip", "geo", "limit", "throttle", "validate", "respond"}
	for _, phase := range phases {
		rule, tied := firstPhaseRule(rules, phase, input.Host, requestPath, input.Method, workingHeaders)
		if tied {
			return stop("incomplete", "ambiguous", phase, "equal-priority matching rules have no guaranteed evaluation order", rule)
		}
		if rule == nil {
			continue
		}
		row := RuleRow{Status: "first_candidate"}
		outcome, reason, preview := previewAction(*rule, row, input, requestPath)
		step := SimulationStep{Phase: phase, RuleID: rule.ID, Kind: phase, Outcome: outcome, PathBefore: requestPath, Reason: reason}
		if preview != nil {
			step.StatusCode, step.Location, step.TargetApp = preview.StatusCode, preview.Location, preview.TargetApp
			step.RedirectHeaders = cloneStringMap(preview.RedirectHeaders)
			step.RetryAfterSeconds, step.Message = preview.RetryAfterSeconds, preview.Message
			step.ValidationField, step.ValidationKeyword = preview.ValidationField, preview.ValidationKeyword
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
			simulation.RequestHeaders = headerSnapshot(workingHeaders)
			return simulation
		case "maintenance":
			if outcome != "maintenance" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome, simulation.StatusCode = "complete", "maintenance", preview.StatusCode
			simulation.RetryAfterSeconds, simulation.Message = preview.RetryAfterSeconds, preview.Message
			simulation.StoppedAt, simulation.Reason = phase, reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "redirect":
			if outcome != "redirect" {
				return stop("incomplete", "unknown", phase, reason, rule)
			}
			simulation.Status, simulation.Outcome = "complete", "redirect"
			simulation.StatusCode, simulation.Location = preview.StatusCode, preview.Location
			simulation.RedirectHeaders = cloneStringMap(preview.RedirectHeaders)
			simulation.StoppedAt, simulation.Reason = phase, reason
			simulation.Steps = append(simulation.Steps, step)
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
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
			applyHeaderOps(workingHeaders, preview.RequestHeaderOps)
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
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
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
			simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
			return simulation
		case "validate":
			switch outcome {
			case "validated", "validation_failed_observe", "validation_failed_warn":
				simulation.ResponseHeaderOps = append(simulation.ResponseHeaderOps, preview.ResponseHeaderOps...)
				step.PathAfter = requestPath
				simulation.Steps = append(simulation.Steps, step)
			case "validation_failed", "unsupported_media_type", "body_too_large":
				simulation.Status, simulation.Outcome = "complete", outcome
				simulation.StatusCode, simulation.StoppedAt, simulation.Reason = preview.StatusCode, phase, reason
				simulation.Steps = append(simulation.Steps, step)
				simulation.FinalPath, simulation.RequestHeaders = requestPath, headerSnapshot(workingHeaders)
				return simulation
			default:
				return stop("incomplete", outcome, phase, reason, rule)
			}
		default:
			return stop("incomplete", "needs_runtime_context", phase, "matching rule depends on gateway runtime state or request data that this trace does not collect", rule)
		}
	}
	simulation.FinalPath = requestPath
	simulation.RequestHeaders = headerSnapshot(workingHeaders)
	return simulation
}

func firstPhaseRule(rules []api.EdgeRuleResponse, kind, host, requestPath, method string, headers http.Header) (*api.EdgeRuleResponse, bool) {
	var first *api.EdgeRuleResponse
	for i := range rules {
		rule := &rules[i]
		if rule.Kind != kind || !rule.Enabled || !HostMatches(rule.MatchHost, host) || !MethodMatches(rule.MatchMethods, method) || !api.EdgeRuleRequestHeadersMatch(rule.MatchHeaders, headers) {
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

func previewAction(rule api.EdgeRuleResponse, row RuleRow, input Input, requestPath string) (string, string, *ActionPreview) {
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
		action, ok := decodeAction[api.EdgeRuleRouteAction](rule.Action, "route")
		if !ok || action.TargetAppSlug == "" {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{Type: "route", TargetApp: action.TargetAppSlug}
		return "route", fmt.Sprintf("would route to app %q", action.TargetAppSlug), preview
	case "rewrite":
		action, ok := decodeAction[api.EdgeRuleRewriteAction](rule.Action, "rewrite")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{Type: "rewrite", Path: requestPath}
		rewritten, applies := api.ApplyEdgeRuleRewritePath(requestPath, action.From, action.To)
		if !applies {
			return "not_applied", fmt.Sprintf("rewrite prefix %q does not match request path %q", action.From, requestPath), preview
		}
		preview.Path = rewritten
		if rewritten == requestPath {
			return "rewrite", fmt.Sprintf("rewrite leaves request path at %q", rewritten), preview
		}
		return "rewrite", fmt.Sprintf("would rewrite request path to %q", rewritten), preview
	case "redirect":
		action, ok := decodeAction[api.EdgeRuleRedirectAction](rule.Action, "redirect")
		if !ok || action.To == "" {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		status := action.StatusCode
		switch status {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			status = http.StatusFound
		}
		preview := &ActionPreview{Type: "redirect", StatusCode: status, Location: action.To, RedirectHeaders: action.Headers}
		return "redirect", fmt.Sprintf("would return HTTP %d redirect to %q", status, action.To), preview
	case "headers":
		action, ok := decodeAction[api.EdgeRuleHeadersAction](rule.Action, "headers")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{
			Type: "headers", RequestHeaderOps: append([]api.EdgeRuleHeaderOp(nil), action.RequestHeaders...),
			ResponseHeaderOps: append([]api.EdgeRuleHeaderOp(nil), action.ResponseHeaders...),
		}
		return "headers", fmt.Sprintf("would apply %d request-header and %d response-header operation(s)", len(action.RequestHeaders), len(action.ResponseHeaders)), preview
	case "maintenance":
		action, ok := decodeAction[api.EdgeRuleMaintenanceAction](rule.Action, "maintenance")
		if !ok {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		retryAfter := action.RetryAfterSeconds
		if retryAfter <= 0 {
			retryAfter = api.EdgeRuleMaintenanceRetryAfterSeconds
		}
		preview := &ActionPreview{Type: "maintenance", StatusCode: http.StatusServiceUnavailable, RetryAfterSeconds: retryAfter, Message: action.Message}
		return "maintenance", fmt.Sprintf("would return HTTP 503 maintenance response (Retry-After %d)", retryAfter), preview
	case "respond":
		action, ok := decodeAction[api.EdgeRuleRespondAction](rule.Action, "respond")
		if !ok || action.StatusCode < http.StatusOK || action.StatusCode > 599 {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		preview := &ActionPreview{Type: "respond", StatusCode: action.StatusCode, Body: append(json.RawMessage(nil), action.Body...)}
		return "fixed_response", fmt.Sprintf("would return fixed HTTP %d response", action.StatusCode), preview
	case "ip":
		if input.ClientIP == "" {
			return "needs_context", "supply client_ip to evaluate this rule", nil
		}
		var envelope struct {
			IP *api.EdgeRuleIPAction `json:"ip"`
		}
		if err := json.Unmarshal(rule.Action, &envelope); err != nil || envelope.IP == nil {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		outcome, reason := evaluateIP(*envelope.IP, net.ParseIP(input.ClientIP))
		return outcome, reason, nil
	case "geo":
		if input.Country == "" {
			return "needs_context", "supply country; trace does not consult the live geo database", nil
		}
		var envelope struct {
			Geo *api.EdgeRuleGeoAction `json:"geo"`
		}
		if err := json.Unmarshal(rule.Action, &envelope); err != nil || envelope.Geo == nil {
			return "unavailable", "rule action is missing or invalid; gateway compilation would drop it", nil
		}
		outcome, reason := evaluateGeo(*envelope.Geo, input.Country)
		return outcome, reason, nil
	case "validate":
		return previewValidateRule(rule, input)
	default:
		return "not_simulated", "this rule kind has runtime behavior outside the action preview", nil
	}
}

func previewValidateRule(rule api.EdgeRuleResponse, input Input) (string, string, *ActionPreview) {
	action, ok := decodeAction[api.EdgeRuleValidateAction](rule.Action, "validate")
	if !ok || len(action.Schema) == 0 {
		return "unavailable", "rule schema is missing or invalid; gateway compilation would reject it", nil
	}
	preview := &ActionPreview{Type: "validate"}
	// Real HTTP parsers remove optional whitespace around field values. The
	// CLI/dashboard parser retains bytes for exact edge-rule header matches,
	// so trim OWS here to mirror the gateway's parsed Content-Type value.
	contentType := strings.TrimSpace(input.Headers.Get("Content-Type"))
	if len(action.ContentTypes) > 0 && !validateContentTypeAllowed(contentType, action.ContentTypes) {
		preview.StatusCode = http.StatusUnsupportedMediaType
		return "unsupported_media_type", "request Content-Type does not match the validation rule", preview
	}
	if !input.BodyProvided {
		return "needs_request_body", "supply --body-file or enable the request body field to evaluate this rule", preview
	}
	bodyLimit := input.RequestBodyMaxBytes
	if action.MaxBodyBytes > 0 && int64(action.MaxBodyBytes) < bodyLimit {
		bodyLimit = int64(action.MaxBodyBytes)
	}
	if int64(len(input.Body)) > bodyLimit {
		preview.StatusCode = http.StatusRequestEntityTooLarge
		return "body_too_large", fmt.Sprintf("request body is %d bytes; the effective validation cap is %d bytes", len(input.Body), bodyLimit), preview
	}
	compiled, err := edgevalidate.Compile(action.Schema, action.RejectOnUnknownFields)
	if err != nil {
		return "unavailable", "validation schema could not be compiled; check the stored edge rule", preview
	}
	fieldErr, err := compiled.Validate(input.Body)
	if err != nil {
		return "unavailable", "validation schema evaluation failed", preview
	}
	if fieldErr == nil {
		return "validated", "request body satisfies the JSON Schema", preview
	}
	preview.ValidationField = fieldErr.Field
	preview.ValidationKeyword = fieldErr.Expected
	mode := rule.ValidateMode
	if mode == "" {
		mode = action.ValidateMode
	}
	if mode == "" {
		mode = api.ValidateModeBlock
	}
	switch mode {
	case api.ValidateModeObserve:
		return "validation_failed_observe", validationFailureReason(fieldErr), preview
	case api.ValidateModeWarn:
		preview.ResponseHeaderOps = []api.EdgeRuleHeaderOp{{Action: "set", Name: "X-Validation-Warning", Value: rule.ID}}
		return "validation_failed_warn", validationFailureReason(fieldErr), preview
	default:
		preview.StatusCode = http.StatusUnprocessableEntity
		return "validation_failed", validationFailureReason(fieldErr), preview
	}
}

func validationFailureReason(fieldErr *edgevalidate.FieldError) string {
	if fieldErr == nil {
		return "request body does not match the JSON Schema"
	}
	if fieldErr.Field == "" {
		return fmt.Sprintf("request body does not match the JSON Schema (keyword %s)", fieldErr.Expected)
	}
	return fmt.Sprintf("request body does not match the JSON Schema at %s (keyword %s)", fieldErr.Field, fieldErr.Expected)
}

// validateContentTypeAllowed mirrors the gateway's content-type check:
// exact media type or the same media type with parameters such as charset.
func validateContentTypeAllowed(contentType string, allowed []string) bool {
	if contentType == "" {
		return false
	}
	for _, candidate := range allowed {
		if candidate == contentType {
			return true
		}
		if index := strings.IndexByte(contentType, ';'); index >= 0 && strings.TrimSpace(contentType[:index]) == candidate {
			return true
		}
	}
	return false
}

func decodeAction[T any](raw json.RawMessage, kind string) (*T, bool) {
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

func evaluateIP(action api.EdgeRuleIPAction, clientIP net.IP) (string, string) {
	deny, ok := parseCIDRs(action.Deny)
	if !ok {
		return "unavailable", "rule contains an invalid deny CIDR; gateway compilation would drop it"
	}
	allow, ok := parseCIDRs(action.Allow)
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

func parseCIDRs(cidrs []string) ([]*net.IPNet, bool) {
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

func evaluateGeo(action api.EdgeRuleGeoAction, country string) (string, string) {
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

func validCountry(country string) bool {
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

func matchedSelectors(rule api.EdgeRuleResponse) string {
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

func headerSnapshot(headers http.Header) map[string][]string {
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

func headerMismatch(expected map[string]string, actual http.Header) string {
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

// HostMatches mirrors the edge-rule store's exact-host and leading-subdomain
// wildcard semantics.
func HostMatches(pattern, host string) bool {
	if pattern == "*" || pattern == host {
		return true
	}
	return strings.HasPrefix(pattern, "*.") && len(host) > len(pattern)-1 && strings.HasSuffix(host, pattern[1:])
}

// MethodMatches compares methods case-insensitively; an empty selector matches
// every method.
func MethodMatches(methods []string, method string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, candidate := range methods {
		if strings.EqualFold(candidate, method) {
			return true
		}
	}
	return false
}

func validRequestHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, ":/[]@?# \t\r\n") {
		return net.ParseIP(host) != nil
	}
	if net.ParseIP(host) != nil {
		return true
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	if len(labels) == 0 || labels[0] == "" {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func normalizeRequestHeaders(headers http.Header) (http.Header, error) {
	normalizedHeaders := make(http.Header, len(headers))
	for name, values := range headers {
		for _, value := range values {
			normalized, err := api.NormalizeEdgeRuleMatchHeaders(map[string]string{name: value})
			if err != nil {
				return nil, err
			}
			for normalizedName := range normalized {
				normalizedHeaders.Add(normalizedName, value)
			}
		}
	}
	return normalizedHeaders, nil
}

func cloneHeaders(headers http.Header) http.Header {
	cloned := make(http.Header, len(headers))
	for name, values := range headers {
		cloned[name] = append([]string(nil), values...)
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func applyHeaderOps(headers http.Header, ops []api.EdgeRuleHeaderOp) {
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
