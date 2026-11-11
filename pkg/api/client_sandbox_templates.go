package api

import (
	"context"
	"net/url"
)

// PutSandboxTemplate creates the named sandbox template, or replaces an
// existing one's start and ready commands (ADR-20261027090013). Build its
// image by deploying a Dockerfile directory to the returned App.
func (c *Client) PutSandboxTemplate(ctx context.Context, name string, req SandboxTemplateRequest) (SandboxTemplate, error) {
	var out SandboxTemplate
	return out, c.do(ctx, "PUT", "/v1/sandbox-templates/"+url.PathEscape(name), req, &out)
}

// GetSandboxTemplate returns one template and its newest build.
func (c *Client) GetSandboxTemplate(ctx context.Context, name string) (SandboxTemplate, error) {
	var out SandboxTemplate
	return out, c.do(ctx, "GET", "/v1/sandbox-templates/"+url.PathEscape(name), nil, &out)
}

// ListSandboxTemplates lists the account's templates by name.
func (c *Client) ListSandboxTemplates(ctx context.Context) (SandboxTemplateList, error) {
	var out SandboxTemplateList
	return out, c.do(ctx, "GET", "/v1/sandbox-templates", nil, &out)
}

// DeleteSandboxTemplate deletes a template and its build app. Running
// sessions keep their copy of the image.
func (c *Client) DeleteSandboxTemplate(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/v1/sandbox-templates/"+url.PathEscape(name), nil, nil)
}
