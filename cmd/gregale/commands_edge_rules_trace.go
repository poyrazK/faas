package main

// A read-only edge-rule simulation. The evaluator is shared with the
// dashboard so both surfaces show the same selectors and outcomes.

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

type edgeRuleTraceResult = edgeruletrace.Result
type edgeRuleTraceRow = edgeruletrace.RuleRow
type edgeRuleTraceSimulation = edgeruletrace.Simulation

func cmdEdgeRulesTrace(args []string) int {
	fs := newFlagSet("edge-rules trace", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	rawURL := fs.String("url", "", "absolute HTTP(S) request URL")
	method := fs.String("method", http.MethodGet, "request method (default GET)")
	clientIP := fs.String("client-ip", "", "simulated client IP for kind=ip rules")
	country := fs.String("country", "", "simulated ISO 3166-1 alpha-2 country for kind=geo rules")
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
	requestPath := u.Path
	if requestPath == "" {
		requestPath = "/"
	}
	requestHeaders, err := edgeruletrace.ParseRequestHeaders(headerArgs)
	if err != nil {
		return printErr("Invalid --header", err)
	}
	input, err := edgeruletrace.NormalizeInput(edgeruletrace.Input{
		App: *slug, Host: u.Hostname(), Path: requestPath, Method: *method,
		ClientIP: *clientIP, Country: *country, Headers: requestHeaders,
	})
	if err != nil {
		return printErr("Invalid trace input", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rules, err := client.ListEdgeRulesForApp(context.Background(), input.App)
	if err != nil {
		return printErr("List failed", err)
	}
	result, err := edgeruletrace.Simulate(input, rules)
	if err != nil {
		return printErr("Invalid trace input", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderEdgeRuleTrace(result)
	return 0
}

func renderEdgeRuleTrace(result edgeruletrace.Result) {
	_, _ = fmt.Fprintf(osStdout, "%s %s%s (app %s)\n", result.Method, result.Host, result.Path, result.App)
	if result.ClientIP != "" || result.Country != "" {
		_, _ = fmt.Fprintf(osStdout, "simulated context: client_ip=%s country=%s\n", emptyAsDash(result.ClientIP), emptyAsDash(result.Country))
	}
	headerNames := sortedHeaderNames(result.Headers)
	for _, name := range headerNames {
		for _, value := range result.Headers[name] {
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
	for _, name := range sortedStringMapKeys(result.Simulation.RedirectHeaders) {
		_, _ = fmt.Fprintf(osStdout, "  redirect header: %s=%q\n", name, result.Simulation.RedirectHeaders[name])
	}
	if result.Simulation.TargetApp != "" {
		_, _ = fmt.Fprintf(osStdout, "  target app: %s\n", result.Simulation.TargetApp)
	}
	for _, op := range result.Simulation.ResponseHeaderOps {
		_, _ = fmt.Fprintf(osStdout, "  response header op: %s %s=%q\n", op.Action, op.Name, op.Value)
	}
	for _, name := range sortedHeaderNames(result.Simulation.RequestHeaders) {
		for _, value := range result.Simulation.RequestHeaders[name] {
			_, _ = fmt.Fprintf(osStdout, "  simulated request header: %s=%q\n", name, value)
		}
	}
	if result.Simulation.StoppedAt != "" {
		_, _ = fmt.Fprintf(osStdout, "  simulation stopped at %s: %s\n", result.Simulation.StoppedAt, result.Simulation.Reason)
	}
	_, _ = fmt.Fprintln(osStdout, result.Scope)
}

func sortedHeaderNames(headers map[string][]string) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedStringMapKeys(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// These aliases preserve compact CLI tests while the implementation itself
// lives in the shared package used by the dashboard.
func previewEdgeRules(app, host, requestPath, method, clientIP, country string, rules []api.EdgeRuleResponse, suppliedHeaders ...http.Header) edgeRuleTraceResult {
	var headers http.Header
	if len(suppliedHeaders) > 0 {
		headers = suppliedHeaders[0]
	}
	result, _ := edgeruletrace.Simulate(edgeruletrace.Input{
		App: app, Host: host, Path: requestPath, Method: method, ClientIP: clientIP, Country: country, Headers: headers,
	}, rules)
	return result
}

func simulateEdgeRuleRequest(host, requestPath, method, clientIP, country string, rules []api.EdgeRuleResponse, headers http.Header) edgeRuleTraceSimulation {
	return previewEdgeRules("test", host, requestPath, method, clientIP, country, rules, headers).Simulation
}

func traceHostMatches(pattern, host string) bool { return edgeruletrace.HostMatches(pattern, host) }
func traceMethodMatches(methods []string, method string) bool {
	return edgeruletrace.MethodMatches(methods, method)
}
func emptyAsDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
