package api

import (
	"context"
	"net/http"
	"net/url"
)

// These are desired logical definitions. Runtime consumer identities and
// production queue-binding row IDs are not part of a stage's configuration.
type ProjectEnvironmentQueueBinding struct {
	Name           string         `json:"name"`
	QueueName      string         `json:"queue_name"`
	Mode           string         `json:"mode"`
	WorkloadClass  string         `json:"workload_class"`
	Enabled        bool           `json:"enabled"`
	MaxConcurrency int            `json:"max_concurrency"`
	RetryPolicy    RetryPolicyDTO `json:"retry_policy"`
}

type ReplaceProjectEnvironmentQueueBindingsRequest struct {
	ExpectedRevision *int64                            `json:"expected_revision"`
	Bindings         *[]ProjectEnvironmentQueueBinding `json:"bindings"`
}

type ProjectEnvironmentQueueBindingsResponse struct {
	Environment      string                           `json:"environment"`
	Workload         string                           `json:"workload"`
	Revision         int64                            `json:"revision"`
	WorkloadRevision int64                            `json:"workload_revision"`
	ConfigHash       string                           `json:"config_hash"`
	ActivationState  string                           `json:"activation_state"`
	Bindings         []ProjectEnvironmentQueueBinding `json:"bindings"`
}

func projectEnvironmentQueuesPath(project, environment, workload string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/workloads/" + url.PathEscape(workload) + "/queue-bindings"
}

func (c *Client) GetProjectEnvironmentQueueBindings(ctx context.Context, project, environment, workload string) (ProjectEnvironmentQueueBindingsResponse, error) {
	var out ProjectEnvironmentQueueBindingsResponse
	return out, c.do(ctx, http.MethodGet, projectEnvironmentQueuesPath(project, environment, workload), nil, &out)
}

func (c *Client) ReplaceProjectEnvironmentQueueBindings(ctx context.Context, project, environment, workload string, request ReplaceProjectEnvironmentQueueBindingsRequest) (ProjectEnvironmentQueueBindingsResponse, error) {
	var out ProjectEnvironmentQueueBindingsResponse
	return out, c.do(ctx, http.MethodPut, projectEnvironmentQueuesPath(project, environment, workload), request, &out)
}

func (c *Client) GetProjectsSlugEnvironmentsEnvironmentWorkloadsWorkloadQueueBindings(ctx context.Context, project, environment, workload string) (ProjectEnvironmentQueueBindingsResponse, error) {
	return c.GetProjectEnvironmentQueueBindings(ctx, project, environment, workload)
}

func (c *Client) PutProjectsSlugEnvironmentsEnvironmentWorkloadsWorkloadQueueBindings(ctx context.Context, project, environment, workload string, request ReplaceProjectEnvironmentQueueBindingsRequest) (ProjectEnvironmentQueueBindingsResponse, error) {
	return c.ReplaceProjectEnvironmentQueueBindings(ctx, project, environment, workload, request)
}
