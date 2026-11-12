package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
)

// EnvironmentRouteBudgetReceipt describes committed policy intent, not current
// configuration or runtime health. The hash binds the exact original request.
type EnvironmentRouteBudgetReceipt struct {
	RequestID     string `json:"request_id"`
	RequestHash   string `json:"request_hash"`
	AppID         string `json:"app_id"`
	Scope         string `json:"scope"`
	ProjectID     string `json:"project_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Revision      int64  `json:"revision"`
	AppRevision   int64  `json:"app_revision"`
	BudgetSource  string `json:"budget_source"`
}

// Preserve the existing native receipt fingerprint byte-for-byte. This is a
// request binding, never a credential or authorization decision.
func EnvironmentRouteBudgetRequestHash(scope string, req SetEnvironmentRouteBudgetsRequest) (string, error) {
	body, err := json.Marshal(struct {
		Scope   string
		Request SetEnvironmentRouteBudgetsRequest
	}{scope, req})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func (c *Client) GetEnvironmentRouteBudgetReceipt(ctx context.Context, slug, environment, requestID string) (EnvironmentRouteBudgetReceipt, error) {
	path := "/v1/apps/" + url.PathEscape(slug) + "/route-budgets/receipts/" + url.PathEscape(requestID)
	if environment == "" {
		path += "?scope=default"
	} else {
		path += "?environment=" + url.QueryEscape(environment)
	}
	var out EnvironmentRouteBudgetReceipt
	err := c.do(ctx, "GET", path, nil, &out)
	return out, err
}
