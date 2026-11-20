package mcpmanagement

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Saved workflow drafts and publishing, and custom domain diagnosis
// (ADR-20261119190000). A draft runs nothing, so saving one is a direct,
// version-guarded write. Publishing makes the draft the version that runs:
// it is a reviewed operation plan that refuses a draft whose saved tests did
// not pass on that exact version, and apid asks a person before a production
// publish. Domain diagnosis is read-only.

type saveWorkflowDraftArgs struct {
	App             string         `json:"app" jsonschema:"App slug"`
	Workflow        string         `json:"workflow" jsonschema:"Saved workflow name"`
	ExpectedVersion int64          `json:"expected_version" jsonschema:"The workflow's current version from gregale_list_workflow_tests; 0 to create it"`
	Definition      map[string]any `json:"definition" jsonschema:"The workflow definition, as gregale workflow get prints it"`
}

type planWorkflowPublishArgs struct {
	App      string `json:"app" jsonschema:"App slug"`
	Workflow string `json:"workflow" jsonschema:"Saved workflow name"`
	Reason   string `json:"reason" jsonschema:"Reviewable reason; at most 280 characters"`
}

type workflowPublishRequest struct {
	Workflow        string `json:"workflow"`
	ExpectedVersion int64  `json:"expected_version"`
}

type domainArgs struct {
	Domain string `json:"domain" jsonschema:"Custom domain, such as shop.example.com"`
}

var domainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9][a-z0-9-]{0,61}[a-z0-9]$`)

func registerWorkflowsDomains(s *mcp.Server, m *management) {
	register(s, m, "gregale_plan_workflow_publish", "Review publishing a saved workflow's draft so it becomes the version that runs. Refused unless the draft's saved tests passed on that exact version. Changes nothing; apply with gregale_apply_operation. On a production app an agent connection asks a person first.", m.planWorkflowPublish)
	register(s, m, "gregale_diagnose_domain", "Explain why a custom domain does or does not serve: DNS record, pointing at Gregale, verification, TLS certificate, CAA and IPv6, each with the exact record to change. Read-only.", m.diagnoseDomain)
	if m.opts.AllowWrites {
		registerTool(s, m, "gregale_save_workflow_draft", "Save a saved workflow's draft definition. A draft runs nothing until published. Fails if the workflow changed since expected_version.", false, m.saveWorkflowDraft)
	}
}

func (m *management) saveWorkflowDraft(ctx context.Context, a saveWorkflowDraftArgs) (any, error) {
	if err := validateSegment(a.Workflow, "workflow"); err != nil {
		return nil, err
	}
	if a.ExpectedVersion < 0 {
		return nil, invalid("invalid_argument", "expected_version is not negative")
	}
	var def api.WorkflowSpec
	if err := strictDecode(a.Definition, &def, "definition"); err != nil {
		return nil, err
	}
	if _, err := m.consentedApp(ctx, a.App, true); err != nil {
		return nil, err
	}
	out, err := m.client.SaveStoredWorkflowDraft(ctx, a.App, a.Workflow, api.SaveStoredWorkflowDraftRequest{ExpectedVersion: a.ExpectedVersion, Definition: def})
	if err != nil {
		return nil, err
	}
	return map[string]any{"workflow": out.Name, "version": out.Version, "published_version": out.PublishedVersion, "next": "Run gregale_run_workflow_tests, then gregale_plan_workflow_publish."}, nil
}

func (m *management) planWorkflowPublish(ctx context.Context, a planWorkflowPublishArgs) (any, error) {
	if err := validateSegment(a.Workflow, "workflow"); err != nil {
		return nil, err
	}
	if _, err := m.consentedApp(ctx, a.App, false); err != nil {
		return nil, err
	}
	w, err := m.client.GetStoredWorkflow(ctx, a.App, a.Workflow)
	if err != nil {
		return nil, err
	}
	if w.PublishedVersion == w.Version {
		return nil, invalid("nothing_to_publish", "the draft is already the published version")
	}
	if w.TestSuite != nil && len(w.TestSuite.Cases) > 0 {
		r := w.TestReport
		if r == nil || r.CheckedVersion != w.Version || !r.Passed {
			return nil, invalid("tests_not_passed", "the draft's saved tests have not passed on this version; run gregale_run_workflow_tests and fix what fails")
		}
	}
	req := workflowPublishRequest{Workflow: a.Workflow, ExpectedVersion: w.Version}
	p, err := m.operationBase(ctx, operationArgs{App: a.App, Reason: a.Reason}, "workflow_publish", req)
	if err != nil {
		return nil, err
	}
	view := map[string]any{"request": req, "draft_version": w.Version, "published_version": w.PublishedVersion, "enabled": w.Enabled, "draft": w.Draft, "published": w.Published}
	if w.TestReport != nil {
		view["test_report"] = map[string]any{"passed": w.TestReport.Passed, "checked_version": w.TestReport.CheckedVersion, "cases": len(w.TestReport.Cases)}
	}
	p.Operation.Changes, _ = json.Marshal(view)
	p.TrafficEffect = "New runs of the workflow use the published draft; runs already in progress finish on the version they started with."
	return m.savePlan(p)
}

func (m *management) runWorkflowPublish(ctx context.Context, p *releasePlan) (any, error) {
	var req workflowPublishRequest
	if err := json.Unmarshal(p.Operation.Request, &req); err != nil {
		return nil, err
	}
	return m.client.PublishStoredWorkflow(ctx, p.App, req.Workflow, api.PublishStoredWorkflowRequest{ExpectedVersion: req.ExpectedVersion})
}

func (m *management) diagnoseDomain(ctx context.Context, a domainArgs) (any, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a.Domain)), ".")
	if len(name) > 253 || !domainPattern.MatchString(name) {
		return nil, invalid("invalid_argument", "domain must be a host name such as shop.example.com")
	}
	d, err := m.client.GetDomain(ctx, name)
	if err != nil {
		return nil, err
	}
	if d.Domain != name || !m.appIDAllowed(d.AppID) {
		return nil, invalid("app_access_denied", "the domain belongs to an app outside this connection")
	}
	report, err := m.client.DomainDoctor(ctx, name)
	if err != nil {
		return nil, err
	}
	failing := []api.DomainDoctorCheck{}
	for _, c := range report.Checks {
		if c.Status == "fail" || c.Status == "pending" {
			failing = append(failing, c)
		}
	}
	return map[string]any{"domain": d, "doctor": report, "needs_attention": failing}, nil
}
