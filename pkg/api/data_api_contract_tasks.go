// adr: 20261027123814 — Data API contract tasks run without the app-task capability.
package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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

const (
	// DataAPIContractArtifactBytes bounds the downloaded, uncompressed artifact.
	DataAPIContractArtifactBytes            = 8 << 20
	DataAPIContractArtifactSHA256Header     = "X-Gregale-Artifact-Sha256"
	DataAPIContractArtifactDeploymentHeader = "X-Gregale-Deployment-ID"
)

const dataAPIContractArtifactArg = "--contract-artifact"

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

// ArtifactRequest reserves space for the artifact and bounded diagnostics on
// the existing task transport. The management API exposes them separately.
func (k DataAPIContractTaskKind) ArtifactRequest() CreateAppTaskRequest {
	request := k.Request()
	if request.Command != nil {
		request.Command = append(request.Command, dataAPIContractArtifactArg)
		request.MaxOutputBytes = DataAPIContractArtifactBytes + 4096
	}
	return request
}

// IsDataAPIContractArtifactTask identifies a fixed contract export that uses the download surface.
func IsDataAPIContractArtifactTask(command []string, shell bool) bool {
	return len(command) > 0 && command[len(command)-1] == dataAPIContractArtifactArg && DataAPIContractTaskKindOf(command, shell) != ""
}

// DataAPIContractTaskKindOf reports which contract kind a task command runs,
// or "" for any other command.
func DataAPIContractTaskKindOf(command []string, shell bool) DataAPIContractTaskKind {
	if shell {
		return ""
	}
	for _, kind := range DataAPIContractTaskKinds {
		if slices.Equal(command, kind.Command()) || slices.Equal(command, kind.ArtifactRequest().Command) {
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
	return c.createDataAPIContractTask(ctx, slug, kind, "")
}

// CreateDataAPIContractArtifactTask requests a downloadable result. An older
// management API can still return the original inline task receipt.
func (c *Client) CreateDataAPIContractArtifactTask(ctx context.Context, slug string, kind DataAPIContractTaskKind) (AppTaskResponse, error) {
	return c.createDataAPIContractTask(ctx, slug, kind, "?output=artifact")
}

func (c *Client) createDataAPIContractTask(ctx context.Context, slug string, kind DataAPIContractTaskKind, query string) (AppTaskResponse, error) {
	var out AppTaskResponse
	return out, c.do(ctx, http.MethodPost, dataAPIContractTasksPath(slug)+query, CreateDataAPIContractTaskRequest{Kind: kind}, &out)
}

func dataAPIContractTasksPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/data-api/contract-tasks"
}

// GetDataAPIContractTask returns the current state or terminal result of one
// contract inspection.
func (c *Client) GetDataAPIContractTask(ctx context.Context, slug, id string) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := dataAPIContractTasksPath(slug) + "/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// DownloadDataAPIContractArtifact verifies the deployment, size and digest
// before returning any bytes to the caller.
func (c *Client) DownloadDataAPIContractArtifact(ctx context.Context, slug, id, deploymentID string) ([]byte, error) {
	path, err := c.projectRoute(dataAPIContractTasksPath(slug) + "/" + url.PathEscape(id) + "?output=artifact")
	if err != nil {
		return nil, err
	}
	req, err := c.newJSONRequest(ctx, http.MethodGet, path, nil, false, "", http.Header{"Accept": {"application/octet-stream"}})
	if err != nil {
		return nil, err
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download Data API contract: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, err := readBoundedResponse(resp, maxResponseBodyBytes)
		if err != nil {
			return nil, err
		}
		return nil, apiErrorFromResponse(resp, body)
	}
	if deploymentID == "" || resp.Header.Get(DataAPIContractArtifactDeploymentHeader) != deploymentID {
		return nil, errors.New("Data API contract download belongs to a different deployment")
	}
	if resp.Header.Get("Content-Type") != "application/octet-stream" || resp.ContentLength > DataAPIContractArtifactBytes {
		return nil, errors.New("Data API contract download has an invalid content type or size")
	}
	body, err := readBoundedResponse(resp, DataAPIContractArtifactBytes)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || resp.Header.Get(DataAPIContractArtifactSHA256Header) != fmt.Sprintf("%x", sha256.Sum256(body)) {
		return nil, errors.New("Data API contract download checksum mismatch")
	}
	return body, nil
}
