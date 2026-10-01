package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) ScanProject(
	ctx context.Context,
	source io.Reader, sourceName, projectSlug, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (PlanResponse, error) {
	return c.ScanProjectWithBinding(ctx, source, sourceName, projectSlug, "", productionBranch,
		installID, only, exclude, persistExclude, noTriggers)
}

// ScanProjectWithBinding is ScanProject with the repository identity needed
// for GitHub push reconciliation.
func (c *Client) ScanProjectWithBinding(
	ctx context.Context,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (PlanResponse, error) {
	return c.ScanProjectWithBindingEnvironment(ctx, source, sourceName, projectSlug, repoFullName, productionBranch,
		installID, only, exclude, persistExclude, noTriggers, "")
}

// ScanProjectWithBindingEnvironment scans a project while selecting a
// registered project environment. The environment is carried through the
// plan token so apply cannot target a different environment than the scan.
func (c *Client) ScanProjectWithBindingEnvironment(
	ctx context.Context,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment string,
	platformTenantRequired ...*bool,
) (PlanResponse, error) {
	var options ProjectDeploymentOptions
	if len(platformTenantRequired) > 0 {
		options.PlatformTenantRequired = platformTenantRequired[0]
	}
	return c.ScanProjectWithBindingOptions(ctx, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment, options)
}

func (c *Client) ScanProjectWithBindingOptions(
	ctx context.Context,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment string,
	options ProjectDeploymentOptions,
) (PlanResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment); err != nil {
		return PlanResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	if err := writeProjectTenantPolicy(w, []*bool{options.PlatformTenantRequired}); err != nil {
		return PlanResponse{}, fmt.Errorf("build tenant policy field: %w", err)
	}
	if options.ComputeRegion != "" {
		if err := w.WriteField("compute_region", options.ComputeRegion); err != nil {
			return PlanResponse{}, fmt.Errorf("build compute region field: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return PlanResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/projects/scan", &b)
	if err != nil {
		return PlanResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	var out PlanResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// ScanProjectSourceRef resolves a connected GitHub installation on the
// control plane and scans the fetched commit without exposing an installation
// token to the CLI.
func (c *Client) ScanProjectSourceRef(ctx context.Context, request ProjectSourceRefScanRequest) (PlanResponse, error) {
	var out PlanResponse
	return out, c.do(ctx, "POST", "/v1/projects/scan/source-ref", request, &out)
}

// ApplyProjectPlan ships the same multipart body as ScanProject without a
// GitHub project binding. It preserves the original SDK shape; callers that
// need push reconciliation should use ApplyProjectPlanWithBinding.
func (c *Client) ApplyProjectPlan(
	ctx context.Context,
	planToken string,
	source io.Reader, sourceName, projectSlug, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (ApplyResponse, error) {
	return c.ApplyProjectPlanWithBinding(ctx, planToken, source, sourceName, projectSlug, "", productionBranch,
		installID, only, exclude, persistExclude, noTriggers)
}

// ApplyProjectPlanWithBinding applies a project plan while carrying the
// repository identity needed for GitHub push reconciliation.
func (c *Client) ApplyProjectPlanWithBinding(
	ctx context.Context,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (ApplyResponse, error) {
	return c.ApplyProjectPlanWithBindingEnvironment(ctx, planToken, source, sourceName, projectSlug, repoFullName, productionBranch,
		installID, only, exclude, persistExclude, noTriggers, "")
}

// ApplyProjectPlanWithBindingEnvironment applies a scanned project to the
// same registered environment that was used during scanning.
func (c *Client) ApplyProjectPlanWithBindingEnvironment(
	ctx context.Context,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment string,
) (ApplyResponse, error) {
	return c.ApplyProjectPlanWithBindingEnvironmentApproval(ctx, planToken, source, sourceName, projectSlug, repoFullName, productionBranch,
		installID, only, exclude, persistExclude, noTriggers, environment, "")
}

// ApplyProjectPlanWithBindingEnvironmentApproval applies a plan and, when
// needed, carries the short-lived approval for a protected environment.
func (c *Client) ApplyProjectPlanWithBindingEnvironmentApproval(
	ctx context.Context,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment, approvalToken string,
	platformTenantRequired ...*bool,
) (ApplyResponse, error) {
	var options ProjectDeploymentOptions
	if len(platformTenantRequired) > 0 {
		options.PlatformTenantRequired = platformTenantRequired[0]
	}
	return c.ApplyProjectPlanWithBindingOptions(ctx, planToken, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment, approvalToken, options)
}

func (c *Client) ApplyProjectPlanWithBindingOptions(
	ctx context.Context,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment, approvalToken string,
	options ProjectDeploymentOptions,
) (ApplyResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment, approvalToken); err != nil {
		return ApplyResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	if err := writeProjectTenantPolicy(w, []*bool{options.PlatformTenantRequired}); err != nil {
		return ApplyResponse{}, fmt.Errorf("build tenant policy field: %w", err)
	}
	if options.ComputeRegion != "" {
		if err := w.WriteField("compute_region", options.ComputeRegion); err != nil {
			return ApplyResponse{}, fmt.Errorf("build compute region field: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return ApplyResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}
	endpoint := c.baseURL + "/v1/projects"
	if planToken != "" {
		endpoint += "?plan_token=" + url.QueryEscape(planToken)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &b)
	if err != nil {
		return ApplyResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	// The public SDK's idempotency transport replaces this fallback with the
	// caller's WithIdempotencyKey value before sending the request.
	req.Header.Set("Idempotency-Key", newUUIDv4())
	var out ApplyResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

func writeProjectTenantPolicy(w *multipart.Writer, policy []*bool) error {
	if len(policy) == 0 || policy[0] == nil {
		return nil
	}
	return w.WriteField("platform_tenant_required", fmt.Sprintf("%t", *policy[0]))
}

// writeProjectMultipartFields serializes the multipart body shared
// by ScanProject + ApplyProjectPlan. The fields exactly mirror the
// OpenAPI ProjectScanRequest schema (the spec-compliance AST gate
// enforces the field-for-field mapping).
func writeProjectMultipartFields(
	w *multipart.Writer, source io.Reader, sourceName, projectSlug,
	repoFullName, productionBranch string, installID int64, only, exclude []string,
	persistExclude, noTriggers bool, environmentAndApproval ...string,
) error {
	fw, err := w.CreateFormFile("source", sourceName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, source); err != nil {
		return err
	}
	if projectSlug != "" {
		if err := w.WriteField("project_slug", projectSlug); err != nil {
			return err
		}
	}
	if repoFullName != "" {
		if err := w.WriteField("repo_full_name", repoFullName); err != nil {
			return err
		}
	}
	if productionBranch != "" {
		if err := w.WriteField("production_branch", productionBranch); err != nil {
			return err
		}
	}
	if installID > 0 {
		if err := w.WriteField("install_id", fmt.Sprintf("%d", installID)); err != nil {
			return err
		}
	}
	if len(only) > 0 {
		if err := w.WriteField("only", strings.Join(only, ",")); err != nil {
			return err
		}
	}
	// ADR-124: inverse-allowlist. Server treats this as a sibling of
	// `only` (lowercased name match). intersect(only, exclude) is
	// rejected at the server with code='exclude_only_overlap'.
	if len(exclude) > 0 {
		if err := w.WriteField("exclude", strings.Join(exclude, ",")); err != nil {
			return err
		}
	}
	// ADR-124 follow-up #3 (PR-B commit 5): write-side persist
	// flag. Server ignores it on the scan path; on the apply path
	// it triggers CreateDeploymentScopeExclusion per excluded slug
	// on a successful apply. Default OFF — the operator's intent
	// is explicit. The field is emitted only when true to keep
	// existing wire captures stable.
	if persistExclude {
		if err := w.WriteField("persist_exclude", "true"); err != nil {
			return err
		}
	}
	if noTriggers {
		if err := w.WriteField("no_triggers", "true"); err != nil {
			return err
		}
	}
	if len(environmentAndApproval) > 0 && strings.TrimSpace(environmentAndApproval[0]) != "" {
		if err := w.WriteField("environment", strings.TrimSpace(environmentAndApproval[0])); err != nil {
			return err
		}
	}
	if len(environmentAndApproval) > 1 && strings.TrimSpace(environmentAndApproval[1]) != "" {
		if err := w.WriteField("approval_token", strings.TrimSpace(environmentAndApproval[1])); err != nil {
			return err
		}
	}
	return nil
}

type ProjectDeploymentOptions struct {
	ComputeRegion          string
	PlatformTenantRequired *bool
}
