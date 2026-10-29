// adr: 20261027123814 — Data API contract tasks run without the app-task capability.
package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"
)

// DataAPIContractTaskKind names one fixed schema inspection the Data API
// runtime runs inside the app's live deployment.
type DataAPIContractTaskKind string

const (
	// DataAPIContractTypes renders the TypeScript contract.
	DataAPIContractTypes DataAPIContractTaskKind = "types"
	// DataAPIContractValidators renders table and RPC input validators.
	DataAPIContractValidators DataAPIContractTaskKind = "validators"
	// DataAPIContractSchema renders the JSON contract snapshot.
	DataAPIContractSchema DataAPIContractTaskKind = "schema"
)

// DataAPIContractTaskKinds lists every contract task kind.
var DataAPIContractTaskKinds = []DataAPIContractTaskKind{DataAPIContractTypes, DataAPIContractValidators, DataAPIContractSchema}

// DataAPIContractTaskTimeoutSeconds bounds one contract inspection.
const DataAPIContractTaskTimeoutSeconds = 60

// Command is the fixed task command for the kind, or nil for an unknown kind.
func (k DataAPIContractTaskKind) Command() []string {
	switch k {
	case DataAPIContractTypes:
		return []string{"node", "/app/types.mjs"}
	case DataAPIContractValidators:
		return []string{"node", "/app/types.mjs", "--validators"}
	case DataAPIContractSchema:
		return []string{"node", "/app/types.mjs", "--snapshot"}
	}
	return nil
}

// Request is the app task the kind admits.
func (k DataAPIContractTaskKind) Request() CreateAppTaskRequest {
	return CreateAppTaskRequest{Command: k.Command(), TimeoutSeconds: DataAPIContractTaskTimeoutSeconds, MaxOutputBytes: AppTaskDefaultMaxOutputBytes}
}

// DataAPIContractTaskKindOf reports which contract kind a task command runs,
// or "" for any other command.
func DataAPIContractTaskKindOf(command []string, shell bool) DataAPIContractTaskKind {
	if shell {
		return ""
	}
	for _, kind := range DataAPIContractTaskKinds {
		if slices.Equal(command, kind.Command()) {
			return kind
		}
	}
	return ""
}

// CreateDataAPIContractTaskRequest selects the contract inspection to run.
type CreateDataAPIContractTaskRequest struct {
	Kind DataAPIContractTaskKind `json:"kind"`
}

// CreateDataAPIContractTask admits one contract inspection against the app's
// live deployment. The receipt is an ordinary app task receipt.
func (c *Client) CreateDataAPIContractTask(ctx context.Context, slug string, kind DataAPIContractTaskKind) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/data-api/contract-tasks"
	return out, c.do(ctx, http.MethodPost, path, CreateDataAPIContractTaskRequest{Kind: kind}, &out)
}

// GetDataAPIContractTask returns the current state or terminal result of one
// contract inspection.
func (c *Client) GetDataAPIContractTask(ctx context.Context, slug, id string) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/data-api/contract-tasks/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}
