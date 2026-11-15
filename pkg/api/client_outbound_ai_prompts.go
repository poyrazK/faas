package api

import (
	"context"
	"net/url"
	"strconv"
)

// Managed AI prompts (ADR 20261114180740).

func outboundAIPromptPath(name string) string {
	return "/v1/outbound/ai-prompts/" + url.PathEscape(name)
}

func (c *Client) ListOutboundAIPrompts(ctx context.Context) (OutboundAIPromptList, error) {
	var out OutboundAIPromptList
	return out, c.do(ctx, "GET", "/v1/outbound/ai-prompts", nil, &out)
}

func (c *Client) GetOutboundAIPrompt(ctx context.Context, name string) (OutboundAIPrompt, error) {
	var out OutboundAIPrompt
	return out, c.do(ctx, "GET", outboundAIPromptPath(name), nil, &out)
}

// PushOutboundAIPromptVersion stores a version, creating the prompt on its
// first one; Created is false when the push matched the latest version.
func (c *Client) PushOutboundAIPromptVersion(ctx context.Context, name string, req PushOutboundAIPromptVersionRequest) (PushOutboundAIPromptVersionResponse, error) {
	var out PushOutboundAIPromptVersionResponse
	return out, c.do(ctx, "POST", outboundAIPromptPath(name)+"/versions", req, &out)
}

func (c *Client) ListOutboundAIPromptVersions(ctx context.Context, name string) (OutboundAIPromptVersionList, error) {
	var out OutboundAIPromptVersionList
	return out, c.do(ctx, "GET", outboundAIPromptPath(name)+"/versions", nil, &out)
}

func (c *Client) GetOutboundAIPromptVersion(ctx context.Context, name string, version int) (OutboundAIPromptVersion, error) {
	var out OutboundAIPromptVersion
	return out, c.do(ctx, "GET", outboundAIPromptPath(name)+"/versions/"+strconv.Itoa(version), nil, &out)
}

// SetOutboundAIPromptLive makes a version live.
func (c *Client) SetOutboundAIPromptLive(ctx context.Context, name string, version int) (OutboundAIPrompt, error) {
	var out OutboundAIPrompt
	return out, c.do(ctx, "PUT", outboundAIPromptPath(name)+"/live", PutOutboundAIPromptLiveRequest{Version: version}, &out)
}

// RollBackOutboundAIPrompt puts the previous live version back.
func (c *Client) RollBackOutboundAIPrompt(ctx context.Context, name string) (OutboundAIPrompt, error) {
	var out OutboundAIPrompt
	return out, c.do(ctx, "POST", outboundAIPromptPath(name)+"/rollback", nil, &out)
}

func (c *Client) DeleteOutboundAIPrompt(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", outboundAIPromptPath(name), nil, nil)
}

func (c *Client) SetOutboundAIPromptCanary(ctx context.Context, name string, req PutOutboundAIPromptCanaryRequest) (OutboundAIPromptCanary, error) {
	var out OutboundAIPromptCanary
	return out, c.do(ctx, "PUT", outboundAIPromptPath(name)+"/canary", req, &out)
}

func (c *Client) PromoteOutboundAIPromptCanary(ctx context.Context, name string) (OutboundAIPrompt, error) {
	var out OutboundAIPrompt
	return out, c.do(ctx, "POST", outboundAIPromptPath(name)+"/canary/promote", nil, &out)
}

func (c *Client) DeleteOutboundAIPromptCanary(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", outboundAIPromptPath(name)+"/canary", nil, nil)
}

func (c *Client) GetOutboundAIPromptStats(ctx context.Context, name string) (OutboundAIPromptStats, error) {
	var out OutboundAIPromptStats
	return out, c.do(ctx, "GET", outboundAIPromptPath(name)+"/stats", nil, &out)
}

func (c *Client) CreateOutboundAIPromptEvaluation(ctx context.Context, name string, req CreateOutboundAIPromptEvaluationRequest) (OutboundAIPromptEvaluation, error) {
	var out OutboundAIPromptEvaluation
	return out, c.do(ctx, "POST", outboundAIPromptPath(name)+"/evaluations", req, &out)
}

func (c *Client) ListOutboundAIPromptEvaluations(ctx context.Context, name string) (OutboundAIPromptEvaluationList, error) {
	var out OutboundAIPromptEvaluationList
	return out, c.do(ctx, "GET", outboundAIPromptPath(name)+"/evaluations", nil, &out)
}

func (c *Client) GetOutboundAIPromptEvaluation(ctx context.Context, evaluationID string) (OutboundAIPromptEvaluation, error) {
	var out OutboundAIPromptEvaluation
	return out, c.do(ctx, "GET", "/v1/outbound/ai-prompt-evaluations/"+url.PathEscape(evaluationID), nil, &out)
}
