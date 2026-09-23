package main

// A read-only, client-side preview of the gateway's static edge-rule
// selectors. This deliberately does not invoke an app or execute rule
// actions: runtime state (IP, country, JWT, cache, budgets, upstreams,
// request body, etc.) cannot be inferred from a URL.

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const edgeRuleTraceScope = "Static host/method/path preview only; gateway compilation, rule actions, rewrites, runtime gates, and final response are not simulated. Equal-priority candidates have no guaranteed order. Results are limited to the named app."

type edgeRuleTraceResult struct {
	App    string             `json:"app"`
	Host   string             `json:"host"`
	Path   string             `json:"path"`
	Method string             `json:"method"`
	Scope  string             `json:"scope"`
	Rules  []edgeRuleTraceRow `json:"rules"`
}

type edgeRuleTraceRow struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Priority     int      `json:"priority"`
	MatchHost    string   `json:"match_host"`
	MatchPath    string   `json:"match_path"`
	MatchMethods []string `json:"match_methods"`
	Status       string   `json:"status"`
	Reason       string   `json:"reason"`
	PrecededBy   string   `json:"preceded_by,omitempty"`
}

func cmdEdgeRulesTrace(args []string) int {
	fs := newFlagSet("edge-rules trace", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	rawURL := fs.String("url", "", "absolute HTTP(S) request URL")
	method := fs.String("method", http.MethodGet, "request method (default GET)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *slug == "" || *rawURL == "" {
		PrintUsage(os.Stderr, "usage: gregale edge-rules trace --app <slug> --url <http(s)://host/path> [--method GET]", "edge-rules")
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
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rules, err := client.ListEdgeRulesForApp(context.Background(), *slug)
	if err != nil {
		return printErr("List failed", err)
	}
	result := previewEdgeRules(*slug, strings.ToLower(u.Hostname()), requestPath, requestMethod, rules)
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "%s %s%s (app %s)\n", result.Method, result.Host, result.Path, result.App)
	if len(result.Rules) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no edge rules for this app)")
	} else {
		for _, row := range result.Rules {
			_, _ = fmt.Fprintf(osStdout, "%-18s %-16s priority=%-5d %s — %s\n", row.Kind, row.Status, row.Priority, row.ID, row.Reason)
		}
	}
	_, _ = fmt.Fprintln(osStdout, result.Scope)
	return 0
}

// previewEdgeRules mirrors the store's enabled host selection and the
// gateway's path.Match / method filters. The app-list API sorts equal
// priorities newest-first, while the gateway host read sorts oldest-first;
// re-sort before identifying the first candidate per kind.
func previewEdgeRules(app, host, requestPath, method string, rules []api.EdgeRuleResponse) edgeRuleTraceResult {
	sorted := append([]api.EdgeRuleResponse(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority < sorted[j].Priority
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})
	result := edgeRuleTraceResult{App: app, Host: host, Path: requestPath, Method: method, Scope: edgeRuleTraceScope, Rules: make([]edgeRuleTraceRow, 0, len(sorted))}
	firstByKind := make(map[string]int)
	for _, rule := range sorted {
		row := edgeRuleTraceRow{
			ID: rule.ID, Kind: rule.Kind, Priority: rule.Priority,
			MatchHost: rule.MatchHost, MatchPath: rule.MatchPath, MatchMethods: rule.MatchMethods,
		}
		switch {
		case !rule.Enabled:
			row.Status, row.Reason = "skipped", "rule is disabled"
		case !traceHostMatches(rule.MatchHost, host):
			row.Status, row.Reason = "skipped", fmt.Sprintf("host %q does not match %q", host, rule.MatchHost)
		case !traceMethodMatches(rule.MatchMethods, method):
			row.Status, row.Reason = "skipped", fmt.Sprintf("method %q is not in %s", method, strings.Join(rule.MatchMethods, ", "))
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
		result.Rules = append(result.Rules, row)
	}
	return result
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
	return fmt.Sprintf("host %q, method %q, path %q match", rule.MatchHost, methods, matchPath)
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
