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
// duplicates on retry. We persist the (repo, sha, check-name) → run ID
// mapping so every phase for one app updates the same Check Run. Subsequent calls hit
// PATCH /repos/{owner}/{repo}/check-runs/{id} instead of POSTing
// a new one.
package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// ChecksWriter is the business seam. The real impl is ChecksAPI;
// tests inject a recording fake.
type ChecksWriter interface {
	WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error
}

// BindingsLookup is the seam that closes review finding #1+#2: it
// maps a repo full-name to the installation_id whose per-install
// access token the Checks writer should use. Production wires it
// to pkg/state.Store.InstallationIDForRepo; tests can pass a stub
// that returns a fixed id or ErrNotFound.
//
// The split-out interface (rather than depending on pkg/state
// directly) keeps the githubd package independent of the apid
// package's persistence layer — a slice 8 architectural decision
// that survives even after the bindings live in Postgres.
type BindingsLookup interface {
	InstallationIDForRepo(ctx context.Context, repoFullName string) (int64, error)
}

type CheckRunStore interface {
	CheckRunID(ctx context.Context, repoFullName, commitSHA, checkName string) (int64, error)
	SaveCheckRunID(ctx context.Context, repoFullName, commitSHA, checkName string, id int64) error
}

// ChecksAPI writes check-runs to api.github.com.
type ChecksAPI struct {
	Tokens    *TokenCache // provides the installation token per installation_id
	HTTP      HTTPClient
	Bindings  BindingsLookup // repo → installation_id (review finding #1+#2 closure)
	CheckRuns CheckRunStore
}

func (c *ChecksAPI) WithCheckRunStore(store CheckRunStore) *ChecksAPI {
	c.CheckRuns = store
	return c
}

// NewChecksAPI builds a ChecksAPI. tokens may be nil for tests
// that don't exercise the HTTP path. bindings may be nil only when
// tokens is also nil — the gRPC checks path always needs both.
// We refuse the (nil, nil) combo explicitly so a missing wiring
// fails fast at startup rather than at first check-run write.
func NewChecksAPI(tokens *TokenCache, hc HTTPClient, bindings BindingsLookup) (*ChecksAPI, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	if tokens == nil && bindings != nil {
		return nil, fmt.Errorf("githubd: ChecksAPI: tokens=nil with bindings!=nil is not a valid configuration")
	}
	return &ChecksAPI{Tokens: tokens, HTTP: hc, Bindings: bindings}, nil
}

// checkRunRequest is the body shape POST /repos/{o}/{r}/check-runs
// expects. We only fill the fields github cares about for the
// commit-icon update.
type checkRunRequest struct {
	Name       string          `json:"name"`
	HeadSHA    string          `json:"head_sha,omitempty"`
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

// writeCheckRun creates the first Check Run for a commit and PATCHes that same
// run for subsequent phases when durable identity storage is configured.
func (c *ChecksAPI) writeCheckRun(ctx context.Context, repoFullName, commitSHA, checkName string, payload checkRunRequest) error {
	token, err := c.tokensForRepo(ctx, repoFullName)
	if err != nil {
		return err
	}
	return c.writeCheckRunWithToken(ctx, token, repoFullName, commitSHA, checkName, payload)
}

func (c *ChecksAPI) writeCheckRunForInstallation(ctx context.Context, installationID int64, repoFullName, commitSHA, checkName string, payload checkRunRequest) error {
	if c.Tokens == nil || installationID <= 0 {
		return fmt.Errorf("githubd: installation-scoped check token is not configured")
	}
	token, err := c.Tokens.Token(ctx, installationID)
	if err != nil {
		return fmt.Errorf("githubd: get install token (install=%d): %w", installationID, err)
	}
	return c.writeCheckRunWithToken(ctx, token, repoFullName, commitSHA, checkName, payload)
}

func (c *ChecksAPI) writeCheckRunWithToken(ctx context.Context, token, repoFullName, commitSHA, checkName string, payload checkRunRequest) error {
	method := http.MethodPost
	endpoint := fmt.Sprintf("%s/repos/%s/check-runs", GitHubAPI, repoFullName)
	existingID := int64(0)
	if c.CheckRuns != nil {
		id, lookupErr := c.CheckRuns.CheckRunID(ctx, repoFullName, commitSHA, checkName)
		switch {
		case lookupErr == nil:
			existingID = id
			method = http.MethodPatch
			endpoint = fmt.Sprintf("%s/repos/%s/check-runs/%d", GitHubAPI, repoFullName, id)
			payload.HeadSHA = "" // update-check-run does not accept head_sha
		case !errors.Is(lookupErr, ErrCheckRunNotFound):
			return lookupErr
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
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
		// Older test doubles returned an empty 201 body. GitHub returns the
		// created object, which is required only when durable identity is wired.
		if c.CheckRuns != nil {
			return fmt.Errorf("githubd: decode check-run response: %w", err)
		}
		return nil
	}
	if out.ID == 0 {
		out.ID = existingID
	}
	if c.CheckRuns != nil && out.ID != 0 {
		if err := c.CheckRuns.SaveCheckRunID(ctx, repoFullName, commitSHA, checkName, out.ID); err != nil {
			return err
		}
	}
	return nil
}

// prodCheckName is the Check Run name stamped by the production
// push path (issue #432 phase 5, issue #739 push-to-deploy).
// Hoisted to a constant so the preview wrapper can reference
// the same name when overriding it for fork-refusal / preview
// status checks (issue #272 / ADR-094).
const prodCheckName = "faas / build"

// previewCheckName is the Check Run name stamped for every
// PR-preview event (issue #272 / ADR-094). GitHub uses the
// (owner, repo, name) tuple as the dedup key for the Checks
// API, so N pushes to the same PR collide on the same Check
// Run rather than spawning N parallel rows in the PR UI.
//
// The name is distinct from the production-push check name
// ("faas / build") so a commit that's both a production push
// AND a PR preview shows up as two separate rows — the
// production row follows the live deploy, the preview row
// follows the preview app's deploy.
const previewCheckName = "gregale-preview"

// previewCheckConclusionNeutral is the Conclusion value
// stamped when the preview path short-circuits without
// success or failure — currently only the D3 fork-PR refusal.
// GitHub renders neutral checks with a grey "—" icon and a
// click-through to the summary, which is what we want: the
// PR author sees "skipped for security" without an alarming
// red ✗.
const previewCheckConclusionNeutral = "neutral"

// WritePreviewCheck posts a Check Run for the PR-preview
// pipeline (issue #272 / ADR-094). The shape mirrors the
// production WriteCheck HTTP call:
//
//   - Same endpoint, headers, token resolver.
//   - Name is pinned to "gregale-preview" so the PR UI groups
//     successive pushes (opened → synchronize → synchronize → …)
//     into one Check Run rather than spawning N parallel rows.
//   - (status, conclusion) follows the phase-derived mapping
//     (WriteCheck handles this), identical to production.
//   - Optional previewURL is appended to the summary as a
//     Markdown link so the PR author can click through.
//
// We hand-roll the request (rather than reuse WriteCheck)
// because WriteCheck hard-codes the production name
// ("faas / build"); threading a name parameter through would
// leak the preview-only concept into the production code path.
func (c *ChecksAPI) WritePreviewCheck(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, previewURL, summary string) error {
	if repoFullName == "" || commitSHA == "" {
		return fmt.Errorf("githubd: repo and sha required for preview check-run")
	}
	fullSummary := summary
	if previewURL != "" {
		// Markdown link shape — GitHub's Check Run summary
		// field is plain text but renders URLs as links when
		// wrapped in Markdown link syntax. Keep the URL
		// outside the link text so a long hostname doesn't
		// blow up the PR UI's summary panel width.
		fullSummary = summary + "\n\nPreview URL: <" + previewURL + ">"
	}
	return c.writeCheckRun(ctx, repoFullName, commitSHA, previewCheckName, checkRunRequest{
		Name:       previewCheckName,
		HeadSHA:    commitSHA,
		Status:     phaseToStatus(phase),
		Conclusion: phaseToConclusion(phase),
		Output: &checkRunOutput{
			Title:   previewPhaseTitle(phase),
			Summary: fullSummary,
		},
		ExternalID: fmt.Sprintf("faas/%s/%s", repoFullName, commitSHA),
	})
}

// previewPhaseTitle maps a CheckPhase to a preview-specific
// title. Mirrors phaseTitle but uses preview-friendly copy so
// the PR UI distinguishes the "Preview queued / building / live"
// lifecycle from the production push pipeline.
func previewPhaseTitle(p githubdgrpc.CheckPhase) string {
	switch p {
	case githubdgrpc.CheckPhaseQueued:
		return "Preview queued"
	case githubdgrpc.CheckPhaseBuilding:
		return "Preview building"
	case githubdgrpc.CheckPhaseLive:
		return "Preview live"
	case githubdgrpc.CheckPhaseFailed:
		return "Preview failed"
	default:
		return "Preview"
	}
}

// WritePreviewCheckForkRefused posts the neutral Check Run
// that announces a fork-PR refusal (ADR-094 D3). It does NOT
// reuse WriteCheck directly because the (status, conclusion)
// pair is (completed, neutral) — the production phase-derived
// mapping only emits success/failure, never neutral. We
// hand-roll a single POST here so the check-run writer can
// stay generic.
//
// The shape mirrors the production WriteCheck HTTP call:
// same endpoint, same headers, same token resolver. The only
// difference is the request body.
//
// summary is the human-readable reason (e.g. "Fork PR
// refused — head repo differs from base repo").
func (c *ChecksAPI) WritePreviewCheckForkRefused(ctx context.Context, repoFullName, commitSHA, summary string) error {
	if repoFullName == "" || commitSHA == "" {
		return fmt.Errorf("githubd: repo and sha required for preview check-run")
	}
	return c.writeCheckRun(ctx, repoFullName, commitSHA, previewCheckName, checkRunRequest{
		Name:       previewCheckName,
		HeadSHA:    commitSHA,
		Status:     statusCompleted,
		Conclusion: previewCheckConclusionNeutral,
		Output: &checkRunOutput{
			Title:   "Preview skipped (security policy)",
			Summary: summary,
		},
		ExternalID: fmt.Sprintf("faas/%s/%s", repoFullName, commitSHA),
	})
}

// WriteCheck writes a check-run for (repo, sha, phase). When CheckRuns is
// configured, later phases PATCH the persisted run ID; test/legacy instances
// without that store retain the original create-per-call behavior.
func (c *ChecksAPI) WriteCheck(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	if repoFullName == "" || commitSHA == "" {
		return fmt.Errorf("githubd: repo and sha required for check-run")
	}
	return c.writeCheckRun(ctx, repoFullName, commitSHA, prodCheckName, checkRunRequest{
		Name:       prodCheckName,
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
}

// WriteSkippedCheckForInstallation posts a completed neutral production check
// when a commit explicitly opts out of deployment with [skip deploy] or
// [deploy skip]. The installation-scoped token keeps the acknowledgement
// least-privilege and matches the push dispatcher's binding resolution.
func (c *ChecksAPI) WriteSkippedCheckForInstallation(ctx context.Context, installationID int64, repoFullName, commitSHA, summary string) error {
	if repoFullName == "" || commitSHA == "" {
		return fmt.Errorf("githubd: repo and sha required for skipped check-run")
	}
	return c.writeCheckRunForInstallation(ctx, installationID, repoFullName, commitSHA, prodCheckName, checkRunRequest{
		Name:       prodCheckName,
		HeadSHA:    commitSHA,
		Status:     statusCompleted,
		Conclusion: previewCheckConclusionNeutral,
		Output: &checkRunOutput{
			Title:   "Deployment skipped",
			Summary: summary,
		},
		ExternalID: fmt.Sprintf("faas/%s/%s", repoFullName, commitSHA),
	})
}

// WriteAppCheck writes the independent lifecycle row for one workload in a
// monorepo. The exact installation ID comes from the authenticated webhook or
// durable deployment binding, avoiding an unscoped repo reverse lookup.
func (c *ChecksAPI) WriteAppCheck(ctx context.Context, installationID int64, repoFullName, commitSHA, appSlug string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	return c.WriteScopedAppCheck(ctx, installationID, repoFullName, commitSHA, appSlug, "", phase, logsURL, summary)
}

// WriteScopedAppCheck writes a workload Check Run whose name and external ID
// include the deployment scope, preventing production and staging updates for
// the same commit from coalescing into one GitHub check.
func (c *ChecksAPI) WriteScopedAppCheck(ctx context.Context, installationID int64, repoFullName, commitSHA, appSlug, scope string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	if repoFullName == "" || commitSHA == "" || appSlug == "" {
		return fmt.Errorf("githubd: repo, sha, and app slug required for app check-run")
	}
	checkName := "gregale / " + appSlug
	externalID := fmt.Sprintf("faas/%s/%s/%s", repoFullName, appSlug, commitSHA)
	if scope != "" {
		checkName += " / " + scope
		externalID = fmt.Sprintf("faas/%s/%s/%s/%s", repoFullName, appSlug, scope, commitSHA)
	}
	return c.writeCheckRunForInstallation(ctx, installationID, repoFullName, commitSHA, checkName, checkRunRequest{
		Name:       checkName,
		HeadSHA:    commitSHA,
		Status:     phaseToStatus(phase),
		Conclusion: phaseToConclusion(phase),
		DetailsURL: logsURL,
		Output: &checkRunOutput{
			Title:   phaseTitle(phase),
			Summary: summary,
		},
		ExternalID: externalID,
	})
}

func (c *ChecksAPI) WritePreviewCheckForInstallation(ctx context.Context, installationID int64, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, previewURL, summary string) error {
	if repoFullName == "" || commitSHA == "" {
		return fmt.Errorf("githubd: repo and sha required for preview check-run")
	}
	if previewURL != "" {
		summary += "\n\nPreview URL: <" + previewURL + ">"
	}
	return c.writeCheckRunForInstallation(ctx, installationID, repoFullName, commitSHA, previewCheckName, checkRunRequest{
		Name:       previewCheckName,
		HeadSHA:    commitSHA,
		Status:     phaseToStatus(phase),
		Conclusion: phaseToConclusion(phase),
		Output:     &checkRunOutput{Title: previewPhaseTitle(phase), Summary: summary},
		ExternalID: fmt.Sprintf("faas/%s/%s", repoFullName, commitSHA),
	})
}

func (c *ChecksAPI) WritePreviewCheckForkRefusedForInstallation(ctx context.Context, installationID int64, repoFullName, commitSHA, summary string) error {
	if repoFullName == "" || commitSHA == "" {
		return fmt.Errorf("githubd: repo and sha required for preview check-run")
	}
	return c.writeCheckRunForInstallation(ctx, installationID, repoFullName, commitSHA, previewCheckName, checkRunRequest{
		Name:       previewCheckName,
		HeadSHA:    commitSHA,
		Status:     statusCompleted,
		Conclusion: previewCheckConclusionNeutral,
		Output:     &checkRunOutput{Title: "Preview skipped (security policy)", Summary: summary},
		ExternalID: fmt.Sprintf("faas/%s/%s", repoFullName, commitSHA),
	})
}

// tokensForRepo resolves the installation token for the repo's
// installation. This used to hardcode installation_id=1 (every
// account shared the same install); review finding #1+#2 forces
// the reverse-lookup via BindingsLookup so each repo gets its own
// install token (or we fail closed with an explicit error rather
// than sending the request as the wrong account).
//
// Returns an error when the BindingsLookup is unset, when no app
// is bound to the repo, or when the per-install token exchange
// fails. We deliberately do NOT fall back to installation_id=1:
// §11 least-privilege forbids one customer's check-run from
// shipping under another customer's installation.
func (c *ChecksAPI) tokensForRepo(ctx context.Context, repoFullName string) (string, error) {
	if c.Tokens == nil {
		return "", fmt.Errorf("githubd: token cache not configured (slice 8)")
	}
	if c.Bindings == nil {
		return "", fmt.Errorf("githubd: bindings lookup not configured (review finding #1+#2)")
	}
	installID, err := c.Bindings.InstallationIDForRepo(ctx, repoFullName)
	if err != nil {
		if errors.Is(err, ErrNoBinding) {
			return "", fmt.Errorf("githubd: no app bound to repo %q (push dropped): %w", repoFullName, err)
		}
		return "", fmt.Errorf("githubd: lookup install id for repo %q: %w", repoFullName, err)
	}
	tok, err := c.Tokens.Token(ctx, installID)
	if err != nil {
		return "", fmt.Errorf("githubd: get install token (install=%d): %w", installID, err)
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

// WritePreviewDestroyComment posts a one-time comment on the PR
// thread the preview was opened against (issue #961 Mega-C PR-1,
// leaf 3). The comment carries a Markdown link to the
// dashboard's one-click destroy action so the PR author (or any
// maintainer with repo access) can tear down the preview without
// having to navigate to the dashboard by hand.
//
// Dedupe is the caller's responsibility — pass a Store seam and
// stamp preview_destroy_commented_at BEFORE posting (mirrors the
// existing previewCheckOnce pattern). Re-posts within a single
// PR's lifetime are silent no-ops; the only reason to re-post is
// a PR close+reopen cycle, and that's intentional — the
// author/maintainer has asked for a fresh state.
//
// Same auth shape as WritePreviewCheck (Tokens + Bindings),
// distinct endpoint: POST /repos/{owner}/{repo}/issues/{n}/comments
// (the issue-comment surface, not the check-run surface).
//
// body is the Markdown body; caller composes it with the
// dashboard URL prefix.
func (c *ChecksAPI) WritePreviewDestroyComment(ctx context.Context, repoFullName string, prNumber int, body string) error {
	if repoFullName == "" || prNumber <= 0 {
		return fmt.Errorf("githubd: repo and pr_number required for preview destroy comment")
	}
	tokens, err := c.tokensForRepo(ctx, repoFullName)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: body})
	if err != nil {
		return fmt.Errorf("githubd: marshal preview destroy comment: %w", err)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/issues/%d/comments", GitHubAPI, repoFullName, prNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
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
		return fmt.Errorf("githubd: write preview destroy comment: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("githubd: write preview destroy comment: status=%d body=%s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// WriteCheckCoalesced is the rate-limit defensive wrapper around
// ChecksAPI.WriteCheck (PR-D / ADR-012 §6 closure). GitHub's
// Checks API caps each install at 1000 calls/hour (100 req/min
// burst); without coalescing, a noisy push loop could trip the
// cap and starve the operator's other PRs of check-runs.
//
// Coalescing rule: per (repo, sha), only POST when the incoming
// phase differs from the last-reported phase. The wrapper holds
// the last phase in an in-memory map keyed by (repo, sha) with
// a janitor that evicts entries older than 1h. The map is
// process-local (a daemon restart resets the state, which is
// safe — the worst case is one extra POST per active (repo,
// sha) at restart, not a rate-limit trip).
//
// Phase transitions that are valid:
//
//	Unspecified → Queued    (first call after enqueue)
//	Queued      → Building  (build started)
//	Building    → Live      (deploy succeeded)
//	Building    → Failed    (deploy failed)
//
// Same-phase re-posts (e.g. retry storms, idempotency replays)
// are silently dropped and the
// `githubd_checks_call_total{status="skipped_coalesced"}` counter
// is incremented so the on-call can see the dedup rate.
//
// Failure semantics: when the underlying WriteCheck returns an
// error, the cache entry is NOT updated so the next call retries
// the same phase. The Prometheus counter
// `githubd_checks_call_total{status="http_error"}` is bumped on
// each error.
var (
	checksCoalesceMu      sync.Mutex
	checksCoalesceCache   = map[string]checksCoalesceEntry{}
	checksCoalesceMinAge  = 1 * time.Hour
	checksCoalesceJanitor = 1 * time.Minute
)

// checksCoalesceEntry tracks the last-reported phase per (repo,
// sha) plus the wall-clock instant of the last successful POST.
// The cachedAt timestamp is what the janitor keys on to evict
// stale entries — without it, the map grows unboundedly across
// hot repos (a single GitHub App install can have thousands of
// active (repo, sha) tuples over its lifetime).
type checksCoalesceEntry struct {
	phase    githubdgrpc.CheckPhase
	cachedAt time.Time
}

// WriteCheckCoalesced wraps ChecksAPI.WriteCheck with per-(repo, sha)
// phase coalescing. Returns nil on a same-phase replay; otherwise
// delegates and caches the new phase on success.
func WriteCheckCoalesced(ctx context.Context, c ChecksWriter, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, logsURL, summary string) error {
	if c == nil {
		return nil
	}
	key := repoFullName + "@" + commitSHA
	checksCoalesceMu.Lock()
	last, seen := checksCoalesceCache[key]
	checksCoalesceMu.Unlock()
	if seen && last.phase == phase {
		checksCallCounter.WithLabelValues("skipped_coalesced").Inc()
		return nil
	}
	err := c.WriteCheck(ctx, repoFullName, commitSHA, phase, logsURL, summary)
	if err != nil {
		checksCallCounter.WithLabelValues("http_error").Inc()
		return err
	}
	checksCoalesceMu.Lock()
	checksCoalesceCache[key] = checksCoalesceEntry{phase: phase, cachedAt: time.Now()}
	checksCoalesceMu.Unlock()
	checksCallCounter.WithLabelValues("posted").Inc()
	return nil
}

// checksCoalesceJanitorLoop evicts entries older than
// checksCoalesceMinAge. Without a janitor the map grows
// unboundedly across a daemon's lifetime (test finding #4).
// The loop runs forever; the daemon is the only process that owns
// the package, so the goroutine exits with the process.
func checksCoalesceJanitorLoop() {
	t := time.NewTicker(checksCoalesceJanitor)
	defer t.Stop()
	for range t.C {
		cutoff := time.Now().Add(-checksCoalesceMinAge)
		checksCoalesceMu.Lock()
		for k, v := range checksCoalesceCache {
			if v.cachedAt.Before(cutoff) {
				delete(checksCoalesceCache, k)
			}
		}
		checksCoalesceMu.Unlock()
	}
}

// checksCallCounter is the Prometheus counter exposed by
// WriteCheckCoalesced. Defined as a package-level var so a test
// can swap it for a recording fake without rewiring the
// production wiring. Registration is wrapped in sync.Once so a
// second package init (e.g. when cmd/githubd imports pkg/githubd
// twice for whitebox tests) is a no-op rather than a panic.
var checksCallCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "githubd_checks_call_total",
	Help: "Outcome of each WriteCheck call after the coalescing wrapper. status=posted|skipped_coalesced|http_error.",
}, []string{"status"})

var checksCallCounterOnce sync.Once

func init() {
	checksCallCounterOnce.Do(func() {
		prometheus.MustRegister(checksCallCounter)
	})
	go checksCoalesceJanitorLoop()
}
