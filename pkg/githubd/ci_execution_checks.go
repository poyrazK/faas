package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/ci"
)

// WriteExecutionCICheck belongs to the gated CI Check worker, for executions
// and skipped sources. Stable external IDs recover lost POST acknowledgements.
func (c *ChecksAPI) WriteExecutionCICheck(ctx context.Context, check ci.ExecutionCheck) (int64, error) {
	if c == nil || c.Tokens == nil || c.HTTP == nil {
		return 0, ci.ErrGitHubUnavailable
	}
	if !check.Valid() {
		return 0, fmt.Errorf("invalid CI execution Check projection")
	}
	token, err := c.Tokens.Token(ctx, check.InstallationID)
	if err != nil {
		return 0, ci.ErrGitHubUnavailable
	}
	id := check.CheckID
	if id == 0 {
		id, err = c.findOwnedCICheck(ctx, token, check.Repository, check.CommitSHA, check.Name(), check.ExternalID())
		if err != nil {
			return 0, err
		}
	}
	endpoint := GitHubAPI + "/repos/" + check.Repository + "/check-runs"
	method := http.MethodPost
	payload := checkRunRequest{Name: check.Name(), ExternalID: check.ExternalID(), HeadSHA: check.CommitSHA, Status: check.Status, Conclusion: check.Conclusion,
		Output: &checkRunOutput{Title: check.Title(), Summary: check.Summary}}
	if id > 0 {
		endpoint += "/" + strconv.FormatInt(id, 10)
		if err := c.verifyExecutionCICheck(ctx, token, endpoint, id, check); err != nil {
			return 0, err
		}
		method, payload.HeadSHA = http.MethodPatch, ""
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode execution Check: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, ci.ErrGitHubUnavailable
	}
	setGitHubHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, ci.ErrGitHubUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := ciGitHubStatus(resp); err != nil {
		return 0, err
	}
	var result checkRunResponse
	if err := decodeGitHubJSON(resp.Body, &result); err != nil {
		return 0, ci.ErrGitHubUnavailable
	}
	if result.ID <= 0 || id > 0 && id != result.ID {
		return 0, fmt.Errorf("CI execution Check response identity mismatch")
	}
	return result.ID, nil
}

// GitHub has no conditional Check update API. Local writers are serialized;
// this extra remote guard rejects an observed newer version or terminal state.
func (c *ChecksAPI) verifyExecutionCICheck(ctx context.Context, token, endpoint string, id int64, check ci.ExecutionCheck) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ci.ErrGitHubUnavailable
	}
	setGitHubHeaders(req, token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ci.ErrGitHubUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if err := ciGitHubStatus(resp); err != nil {
		return err
	}
	var remote struct {
		ID         int64          `json:"id"`
		Name       string         `json:"name"`
		HeadSHA    string         `json:"head_sha"`
		ExternalID string         `json:"external_id"`
		Status     string         `json:"status"`
		Output     checkRunOutput `json:"output"`
	}
	if err := decodeGitHubJSON(resp.Body, &remote); err != nil {
		return ci.ErrGitHubUnavailable
	}
	if remote.ID != id || remote.Name != check.Name() || remote.HeadSHA != check.CommitSHA || remote.ExternalID != check.ExternalID() {
		return fmt.Errorf("CI execution Check ownership mismatch")
	}
	version, err := executionCICheckVersion(remote.Output.Title)
	if err != nil || version > check.Version || remote.Status == "completed" && check.Status != "completed" {
		return fmt.Errorf("CI execution Check remote version conflict")
	}
	return nil
}

func executionCICheckVersion(title string) (int64, error) {
	_, tail, ok := strings.Cut(title, " (version ")
	if !ok || !strings.HasPrefix(title, "Gregale CI: ") || !strings.HasSuffix(tail, ")") {
		return 0, ci.ErrGitHubUnavailable
	}
	version, err := strconv.ParseInt(strings.TrimSuffix(tail, ")"), 10, 64)
	if err != nil || version <= 0 {
		return 0, ci.ErrGitHubUnavailable
	}
	return version, nil
}
