package api

import "context"

// BrowserPromotionApprovalRequest starts human review, without approving or
// changing a release. Reason is displayed as untrusted agent-provided text.
type BrowserPromotionApprovalRequest struct {
	PromotionToken string `json:"promotion_token"`
	Reason         string `json:"reason"`
}

// BrowserPromotionApprovalResponse carries a public review link and private
// exchange material. MCP callers must only receive ReviewURL and ExpiresAt.
type BrowserPromotionApprovalResponse struct {
	ReviewURL    string `json:"review_url"`
	Ticket       string `json:"ticket"`
	ExchangeCode string `json:"exchange_code"`
	ExpiresAt    string `json:"expires_at"`
}

type BrowserPromotionApprovalExchange struct {
	Ticket       string `json:"ticket"`
	ExchangeCode string `json:"exchange_code"`
}

func (c *Client) RequestBrowserPromotionApproval(ctx context.Context, project, environment string, input BrowserPromotionApprovalRequest) (BrowserPromotionApprovalResponse, error) {
	var out BrowserPromotionApprovalResponse
	err := c.do(ctx, "POST", "/v1/projects/"+project+"/environments/"+environment+"/approval-requests", input, &out)
	return out, err
}

func (c *Client) ExchangeBrowserPromotionApproval(ctx context.Context, project, environment string, input BrowserPromotionApprovalExchange) (ProjectEnvironmentApprovalResponse, error) {
	var out ProjectEnvironmentApprovalResponse
	err := c.do(ctx, "POST", "/v1/projects/"+project+"/environments/"+environment+"/approval-requests/exchange", input, &out)
	return out, err
}
