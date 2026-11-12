package api

import (
	"context"
	"net/http"
	"net/url"
)

// ResumeEnvironmentAutomation enables the automation a full clone paused in
// one stage of a project.
func (c *Client) ResumeEnvironmentAutomation(ctx context.Context, project, environment string) (ResumeEnvironmentAutomationResponse, error) {
	var out ResumeEnvironmentAutomationResponse
	err := c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/environments/"+url.PathEscape(environment)+"/automation/resume", nil, &out)
	return out, err
}
