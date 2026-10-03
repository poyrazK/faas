package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func readPreviewRouteRequirements(path string) (routerequirements.Config, string, error) {
	body, digest, err := readRouteRequirementsDocument(path)
	if err != nil {
		return routerequirements.Config{}, "", err
	}
	config, err := routerequirements.Parse(body)
	return config, digest, err
}

func readPreviewCoverageRequirements(path string) (routerequirements.PreviewConfig, string, error) {
	body, digest, err := readRouteRequirementsDocument(path)
	if err != nil {
		return routerequirements.PreviewConfig{}, "", err
	}
	config, err := routerequirements.ParsePreview(body)
	return config, digest, err
}

func readRouteRequirementsDocument(path string) ([]byte, string, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("open route requirements: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteRequirementsMaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read route requirements: %w", err)
	}
	return body, fmt.Sprintf("%x", sha256.Sum256(body)), nil
}

func attachPreviewRouteRequirements(ctx context.Context, client *api.Client, report *previewRouteReport, config routerequirements.Config, digest string) {
	evidence := loadPreviewRouteRequirementsContext(ctx, client, report.Preview, report.candidateEdgeRules, report.candidateRulesLoaded, report.candidateRulesErr)
	result := routerequirements.WrapReport(routerequirements.Evaluate(config, digest, evidence))
	report.Requirements = &result
}

func attachPreviewCoverageRequirements(ctx context.Context, client *api.Client, report *previewRouteReport, config routerequirements.PreviewConfig, digest string) {
	evidence := loadPreviewRouteRequirementsContext(ctx, client, report.Preview, report.candidateEdgeRules, report.candidateRulesLoaded, report.candidateRulesErr)
	inventory := routerequirements.CandidateInventory(report.candidateContract, report.CandidateDeployment, report.CandidateDocumentHash)
	result := routerequirements.EvaluatePreview(config, digest, evidence, inventory)
	report.Requirements = &result
}

func loadRouteRequirementsContext(ctx context.Context, client *api.Client, slug string) routerequirements.Context {
	return loadPreviewRouteRequirementsContext(ctx, client, slug, nil, false, nil)
}

func loadPreviewRouteRequirementsContext(ctx context.Context, client *api.Client, slug string, candidateRules []api.EdgeRuleResponse, rulesLoaded bool, rulesErr error) routerequirements.Context {
	evidence := routerequirements.Context{App: api.AppResponse{Slug: slug}}
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		evidence.Unavailable = "app:" + previewReportReadReason(err)
	} else if app.Slug != slug || app.ID == "" {
		evidence.Unavailable = "app_identity_unavailable"
	} else {
		evidence.App = app
		evidence.Host, evidence.Unavailable = previewRequirementsHost(app)
	}
	if evidence.Unavailable == "" {
		if rulesLoaded {
			evidence.Rules, err = candidateRules, rulesErr
		} else {
			evidence.Rules, err = client.ListEdgeRulesForApp(ctx, slug)
		}
		if err != nil {
			evidence.Unavailable = "rules:" + previewReportReadReason(err)
		}
		seen := map[string]bool{}
		for _, rule := range evidence.Rules {
			if rule.AppID != app.ID || rule.ID == "" || seen[rule.ID] {
				evidence.Unavailable = "rule_identity_unavailable"
				break
			}
			seen[rule.ID] = true
		}
	}
	return evidence
}

func previewRequirementsHost(app api.AppResponse) (string, string) {
	u, err := url.Parse(app.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Port() != "" || (u.Path != "" && u.Path != "/") {
		return "", "platform_host_unavailable"
	}
	host := strings.ToLower(u.Hostname())
	if _, err := edgeruletrace.NormalizeInput(edgeruletrace.Input{App: app.Slug, Host: host, Method: "GET", Path: "/"}); err != nil {
		return "", "platform_host_unavailable"
	}
	return host, ""
}

func renderPreviewRequirements(w io.Writer, report *routerequirements.PreviewReport, markdown bool) {
	if report == nil {
		return
	}
	if markdown {
		_, _ = fmt.Fprint(w, "\n### Route requirements\n\n")
	} else {
		_, _ = fmt.Fprint(w, "\nRoute requirements\n")
	}
	_, _ = fmt.Fprintf(w, "Status: %s; host: %s; policy: %s\n\n", report.Status, previewReportText(report.Host), report.PolicyScope)
	if report.Coverage != nil {
		_, _ = fmt.Fprintf(w, "Candidate inventory: %s; source: %s; deployment: %s; operations: %d; reason: %s\n\n", report.Coverage.Status, report.Coverage.Source, previewReportText(report.Coverage.Deployment), report.Coverage.RouteCount, report.Coverage.Code)
		for _, group := range report.Groups {
			name := previewReportText(group.Name)
			if markdown {
				name = mdText(name)
			}
			_, _ = fmt.Fprintf(w, "Group %s: %s; matched: %d; public exceptions: %d; reason: %s\n", name, group.Status, group.MatchedRoutes, group.ExemptRoutes, group.Code)
		}
		_, _ = fmt.Fprintln(w)
	}
	if markdown {
		_, _ = fmt.Fprintln(w, "| Route | Requirement | Result | Expected | Actual | Rule IDs |")
		_, _ = fmt.Fprintln(w, "|---|---|---|---|---|---|")
	}
	checkGroups := map[string]map[int]string{}
	for _, assignment := range report.Assignments {
		key := previewReportRouteKey(assignment.Method, assignment.Path)
		checkGroups[key] = map[int]string{}
		for _, check := range assignment.Checks {
			checkGroups[key][check.CheckIndex] = check.Group
		}
	}
	for _, route := range report.Routes {
		for index, finding := range route.Checks {
			key := previewReportRouteKey(route.Method, route.Path)
			requirement := finding.Requirement
			if group := checkGroups[key][index]; group != "" {
				requirement += " (group: " + group + ")"
			}
			fields := []string{key, requirement, finding.Status + " (" + finding.Code + ")", finding.Expected, finding.Actual, strings.Join(finding.RuleIDs, ", ")}
			for i := range fields {
				fields[i] = previewReportText(fields[i])
				if markdown {
					fields[i] = mdCell(fields[i])
				}
			}
			if markdown {
				_, _ = fmt.Fprintf(w, "| %s |\n", strings.Join(fields, " | "))
			} else {
				_, _ = fmt.Fprintf(w, "%s: %s %s\n  expected: %s\n  actual: %s; rules: %s\n", fields[0], fields[1], fields[2], fields[3], fields[4], fields[5])
			}
		}
	}
	_, _ = fmt.Fprintln(w)
	for _, route := range report.Routes {
		for _, finding := range route.Checks {
			text := previewReportRouteKey(route.Method, route.Path) + " / " + finding.Requirement + ": " + finding.Reason
			if finding.NextAction != "" {
				text += " " + finding.NextAction
			}
			text = previewReportText(text)
			if markdown {
				text = mdText(text)
			}
			_, _ = fmt.Fprintf(w, "- %s\n", text)
		}
	}
	_, _ = fmt.Fprintf(w, "\n%s\n", report.Scope)
}
