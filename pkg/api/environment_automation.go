package api

import (
	"context"
	"net/http"
	"net/url"
)

// ResumeEnvironmentAutomationResponse reports the automation a resume
// enabled in one project environment: the crons a full clone copied paused
// that the source ran (ADR-20261104120000). A cron the source had disabled,
// or one already enabled or disabled since, is left as it is.
type ResumeEnvironmentAutomationResponse struct {
	Environment  string   `json:"environment"`
	CronsResumed int      `json:"crons_resumed"`
	CronIDs      []string `json:"cron_ids"`
}

// ResumeEnvironmentAutomation enables the automation a full clone paused in
// one stage of a project.
func (c *Client) ResumeEnvironmentAutomation(ctx context.Context, project, environment string) (ResumeEnvironmentAutomationResponse, error) {
	var out ResumeEnvironmentAutomationResponse
	err := c.do(ctx, http.MethodPost, "/v1/projects/"+url.PathEscape(project)+"/environments/"+url.PathEscape(environment)+"/automation/resume", nil, &out)
	return out, err
}
