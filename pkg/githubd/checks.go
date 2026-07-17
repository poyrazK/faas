// Checks API writer (slice 8, ADR-012).
//
// Every state transition in the build pipeline writes a check-run
// back to GitHub so the commit's "✓" / "✗" icon updates
// immediately. The phase mapping is:
//
//	CheckPhaseQueued    → "queued"
//	CheckPhaseBuilding  → "in_progress"
//	CheckPhaseLive      → "completed" / "success"
//	CheckPhaseFailed    → "completed" / "failure"
//
// GitHub requires idempotent check-run writes to avoid creating
// duplicates on retry. We use the (repo, sha, phase) tuple as the
// dedup key — the same phase transition for the same commit is
// always the same check-run; subsequent calls hit
// PATCH /repos/{owner}/{repo}/check-runs/{id} instead of POSTing
// a new one.
//
// Idempotency storage lives in pkg/state (slice 8 adds the table
// to migration 00006).
package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// ChecksWriter is the business seam. The real impl is ChecksAPI;
// tests inject a recording fake.
type ChecksWriter interface {
	WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error
}

// ChecksAPI writes check-runs to api.github.com.
type ChecksAPI struct {
	Tokens *TokenCache // provides the installation token per repo
	HTTP   HTTPClient
}

// NewChecksAPI builds a ChecksAPI. tokens may be nil for tests
// that don't exercise the HTTP path.
func NewChecksAPI(tokens *TokenCache, hc HTTPClient) *ChecksAPI {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &ChecksAPI{Tokens: tokens, HTTP: hc}
}

// checkRunRequest is the body shape POST /repos/{o}/{r}/check-runs
// expects. We only fill the fields github cares about for the
// commit-icon update.
type checkRunRequest struct {
	Name       string          `json:"name"`
	HeadSHA    string          `json:"head_sha"`
	Status     string          `json:"status"`
	Conclusion string          `json:"conclusion,omitempty"`
	DetailsURL string          `json:"details_url,omitempty"`
	Output     *checkRunOutput `json:"output,omitempty"`
	ExternalID string          `json:"external_id,omitempty"`
}

type checkRunOutput struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// checkRunResponse is the shape GitHub returns from POST/PATCH.
type checkRunResponse struct {
	ID int64 `json:"id"`
}

// WriteCheck posts a check-run for (repo, sha, phase). Idempotency
// is the caller's responsibility — this method always creates a
// new check-run; the StateStore-wrapped variant (NewStatefulChecks)
// is the one slice 8 callers should use.
func (c *ChecksAPI) WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	if repoFullName == "" || commitSHA == "" {
		return fmt.Errorf("githubd: repo and sha required for check-run")
	}
	tokens, err := c.tokensForRepo(ctx, repoFullName)
	if err != nil {
		return err
	}
	body, err := json.Marshal(checkRunRequest{
		Name:       "faas / build",
		HeadSHA:    commitSHA,
		Status:     phaseToStatus(phase),
		Conclusion: phaseToConclusion(phase),
		DetailsURL: logsURL,
		Output: &checkRunOutput{
			Title:   phaseTitle(phase),
			Summary: summary,
		},
		ExternalID: fmt.Sprintf("faas/%s/%s", repoFullName, commitSHA),
	})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/check-runs", GitHubAPI, repoFullName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tokens)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "faas-githubd/1.0")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("githubd: write check-run: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("githubd: write check-run: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var out checkRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("githubd: decode check-run response: %w", err)
	}
	return nil
}

// tokensForRepo resolves the installation token for the repo's
// installation. Today's slice-8 design assumes a single install per
// account; the per-repo install_id lookup will land in the
// bindings-store slice 8 work.
func (c *ChecksAPI) tokensForRepo(ctx context.Context, _ string) (string, error) {
	if c.Tokens == nil {
		return "", fmt.Errorf("githubd: token cache not configured (slice 8)")
	}
	// Single-install-per-account v1.0: use installation_id = 1 as
	// the placeholder until the bindings store carries the real id.
	tok, err := c.Tokens.Token(ctx, 1)
	if err != nil {
		return "", fmt.Errorf("githubd: get install token: %w", err)
	}
	return tok, nil
}

const (
	statusQueued     = "queued"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"
)

func phaseToStatus(p githubdgrpc.CheckPhase) string {
	switch p {
	case githubdgrpc.CheckPhaseQueued:
		return statusQueued
	case githubdgrpc.CheckPhaseBuilding:
		return statusInProgress
	case githubdgrpc.CheckPhaseLive:
		return statusCompleted
	case githubdgrpc.CheckPhaseFailed:
		return statusCompleted
	default:
		return statusQueued
	}
}

func phaseToConclusion(p githubdgrpc.CheckPhase) string {
	switch p {
	case githubdgrpc.CheckPhaseLive:
		return "success"
	case githubdgrpc.CheckPhaseFailed:
		return "failure"
	default:
		return ""
	}
}

func phaseTitle(p githubdgrpc.CheckPhase) string {
	switch p {
	case githubdgrpc.CheckPhaseQueued:
		return "Build queued"
	case githubdgrpc.CheckPhaseBuilding:
		return "Build in progress"
	case githubdgrpc.CheckPhaseLive:
		return "Deployment live"
	case githubdgrpc.CheckPhaseFailed:
		return "Deployment failed"
	default:
		return "faas build"
	}
}

// _ pins time so a future refactor that drops the import on
// unused-token usage doesn't drop it prematurely.
var _ = time.Time{}
