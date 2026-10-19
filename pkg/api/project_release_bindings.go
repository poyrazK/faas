package api

import (
	"context"
	"net/http"
	"time"
)

// A prepared manifest is an immutable selection and compare-and-swap intent,
// not an evidence grant. Activation independently reads and checks all facts.
type CheckedProjectReleaseManifest struct {
	Deactivate                  bool                         `json:"deactivate,omitempty"`
	RollbackReleaseID           string                       `json:"rollback_release_id,omitempty"`
	Project                     string                       `json:"project"`
	Environment                 string                       `json:"environment"`
	TTLSeconds                  int                          `json:"ttl_seconds"`
	Deployments                 map[string]string            `json:"deployments"`
	ExpectedActiveReleaseID     *string                      `json:"expected_active_release_id"`
	ExpectedFallbackDeployments map[string]string            `json:"expected_fallback_deployments"`
	ExpectedPolicyRevisions     map[string]int64             `json:"expected_policy_revisions"`
	ServiceTargets              map[string]map[string]string `json:"service_targets"`
}

type CheckedProjectReleaseResponse struct {
	Release        ProjectReleaseSetResponse     `json:"release"`
	BindingsChecks map[string]BindingCheckReport `json:"bindings_checks"`
}

func (c *Client) PrepareCheckedProjectRelease(ctx context.Context, project, environment string, request PublishProjectReleaseSetRequest) (CheckedProjectReleaseManifest, error) {
	var out CheckedProjectReleaseManifest
	return out, c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/prepare", request, &out)
}

func (c *Client) ActivateCheckedProjectRelease(ctx context.Context, project, environment string, manifest CheckedProjectReleaseManifest) (CheckedProjectReleaseResponse, error) {
	var out CheckedProjectReleaseResponse
	return out, c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/activate", manifest, &out)
}

// PrepareProjectRollbackRequest selects retained history, never arbitrary members.
type PrepareProjectRollbackRequest struct {
	ReleaseID  string `json:"release_id"`
	TTLSeconds int    `json:"ttl_seconds"`
}

func (c *Client) PrepareCheckedProjectRollback(ctx context.Context, project, environment string, request PrepareProjectRollbackRequest) (CheckedProjectReleaseManifest, error) {
	var out CheckedProjectReleaseManifest
	return out, c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/prepare-rollback", request, &out)
}

func (c *Client) RollbackCheckedProjectRelease(ctx context.Context, project, environment string, manifest CheckedProjectReleaseManifest) (CheckedProjectReleaseResponse, error) {
	var out CheckedProjectReleaseResponse
	return out, c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/rollback", manifest, &out)
}

// ProjectReleaseDeactivationReceipt records the checked return to weighted routes.
type ProjectReleaseDeactivationReceipt struct {
	ID              string                            `json:"id"`
	ReleaseID       string                            `json:"release_id"`
	CompletedAt     time.Time                         `json:"completed_at"`
	FallbackMembers []ProjectReleaseSetMemberResponse `json:"fallback_members"`
}

func (c *Client) PrepareCheckedProjectDeactivation(ctx context.Context, project, environment string) (CheckedProjectReleaseManifest, error) {
	var out CheckedProjectReleaseManifest
	return out, c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/prepare-deactivation", nil, &out)
}
func (c *Client) DeactivateCheckedProjectRelease(ctx context.Context, project, environment string, manifest CheckedProjectReleaseManifest) (CheckedProjectReleaseResponse, error) {
	var out CheckedProjectReleaseResponse
	return out, c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/deactivate", manifest, &out)
}
