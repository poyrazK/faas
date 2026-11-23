package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

// VerificationContract is an app's gregale.verify.yaml in its validated JSON
// form (ADR-20261122220000). Gregale runs it against every new deployment of
// the app before the deployment takes traffic.
type VerificationContract struct {
	AppID          string          `json:"app_id"`
	Contract       json.RawMessage `json:"contract"`
	ContractSHA256 string          `json:"contract_sha256"`
	CheckCount     int             `json:"check_count"`
	Revision       int64           `json:"revision"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// PutVerificationContractRequest replaces an app's contract. A non-nil
// ExpectedRevision must equal the stored revision (0 when there is none).
type PutVerificationContractRequest struct {
	Contract         json.RawMessage `json:"contract"`
	ExpectedRevision *int64          `json:"expected_revision,omitempty"`
}

func verificationContractPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/verification-contract"
}

func (c *Client) GetVerificationContract(ctx context.Context, slug string) (VerificationContract, error) {
	var out VerificationContract
	err := c.do(ctx, "GET", verificationContractPath(slug), nil, &out)
	return out, err
}

func (c *Client) PutVerificationContract(ctx context.Context, slug string, req PutVerificationContractRequest) (VerificationContract, error) {
	var out VerificationContract
	err := c.do(ctx, "PUT", verificationContractPath(slug), req, &out)
	return out, err
}

// DeleteVerificationContract removes the app's contract; a non-nil
// expectedRevision must match the stored revision.
func (c *Client) DeleteVerificationContract(ctx context.Context, slug string, expectedRevision *int64) error {
	path := verificationContractPath(slug)
	if expectedRevision != nil {
		path += "?expected_revision=" + strconv.FormatInt(*expectedRevision, 10)
	}
	return c.do(ctx, "DELETE", path, nil, nil)
}
