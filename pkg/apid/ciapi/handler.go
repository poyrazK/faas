// Package ciapi serves the internal Gregale CI API. apidserver
// owns authentication, admin scopes and MFA; Config adds an account allowlist.
package ciapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

type Handler struct {
	Config         ci.Config
	Store          ci.Store
	Audit          func(context.Context, string, *string, map[string]any)
	VerifyGitHub   ci.GitHubVerifier
	ExecutionReady bool
}

type Action func(Handler, http.ResponseWriter, *http.Request, state.Account)

func (h Handler) available(w http.ResponseWriter, acct state.Account) bool {
	if !h.Config.Allows(acct.ID) || h.Store == nil {
		httpkit.NoSuchRoute(w)
		return false
	}
	return true
}

func (h Handler) Capabilities(w http.ResponseWriter, _ *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{
		"maturity": "internal", "admission_available": true,
		"compute_available": false, "dispatch_available": false,
		"blocked_reason":        ci.ComputeUnavailable,
		"github_enabled":        h.Config.GitHubEnabled,
		"github_checks_enabled": h.Config.GitHubEnabled && h.Config.GitHubChecksEnabled,
		"max_jobs":              api.CIMaxJobs, "max_steps_per_job": api.CIMaxStepsPerJob,
		"max_matrix_variants_per_job": api.CIMaxMatrixVariantsPerJob,
		"max_definition_bytes":        api.CIDefinitionMaxBytes,
		"max_revisions":               api.CIMaxRevisionsPerAccount, "max_runs": api.CIMaxRunsPerAccount,
	})
}

func definition(w http.ResponseWriter, r *http.Request) (ci.Definition, bool) {
	var req struct {
		Definition string `json:"definition"`
	}
	if !httpkit.DecodeJSONLimit(w, r, &req, api.CIRequestMaxBytes) {
		return ci.Definition{}, false
	}
	def, err := ci.Parse([]byte(req.Definition))
	if err != nil {
		writeDefinitionError(w, err)
		return ci.Definition{}, false
	}
	return def, true
}

func writeDefinitionError(w http.ResponseWriter, err error) {
	problem := api.ErrValidation(err.Error())
	var limit *ci.LimitError
	if errors.As(err, &limit) {
		problem = problem.WithLimit(int64(limit.Limit), int64(limit.Observed)).WithDocs("/docs/ops/internal-ci")
	}
	api.WriteProblem(w, problem)
}

func (h Handler) Validate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	def, ok := definition(w, r)
	if !ok {
		return
	}
	plan, _, err := ci.Compile(def)
	if err != nil {
		writeDefinitionError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, plan)
}

func (h Handler) PutRevision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	if !ci.ValidName(r.PathValue("name")) {
		api.WriteProblem(w, api.ErrValidation("invalid CI pipeline name"))
		return
	}
	def, ok := definition(w, r)
	if !ok {
		return
	}
	row, err := h.Store.PutRevision(r.Context(), acct.ID, r.PathValue("name"), def)
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.revision.saved", map[string]any{"revision_id": row.ID, "digest": row.Digest})
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) ListRevisions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	name := r.PathValue("name")
	if name != "" && !ci.ValidName(name) {
		api.WriteProblem(w, api.ErrValidation("invalid CI pipeline name"))
		return
	}
	rows, err := h.Store.ListRevisions(r.Context(), acct.ID, name)
	if err != nil {
		writeError(w, err)
		return
	}
	// List receipts omit command bodies; detail reads retain the full definition.
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "name": row.Name, "digest": row.Digest, "created_at": row.CreatedAt})
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"revisions": items})
}

func validID(w http.ResponseWriter, id string) bool {
	if !ci.ValidID(id) {
		httpkit.NotFound(w, "CI resource not found")
		return false
	}
	return true
}

func (h Handler) Revision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := h.Store.Revision(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) Submit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	var req ci.SubmitRequest
	if !httpkit.DecodeJSONLimit(w, r, &req, api.CIRequestMaxBytes) {
		return
	}
	if !validID(w, req.RevisionID) {
		return
	}
	if !ci.ValidCommitSHA(req.CommitSHA) || !ci.ValidIdempotencyKey(r.Header.Get("Idempotency-Key")) {
		api.WriteProblem(w, api.ErrValidation("CI requires a full lowercase commit SHA and a bounded printable Idempotency-Key"))
		return
	}
	row, replay, err := h.Store.Submit(r.Context(), acct.ID, r.Header.Get("Idempotency-Key"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		h.audit(r, acct.ID, "ci.run.blocked", map[string]any{"run_id": row.ID, "revision_id": row.RevisionID, "reason": ci.ComputeUnavailable})
	}
	httpkit.WriteJSON(w, http.StatusAccepted, row)
}

func (h Handler) ListRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	rows, err := h.Store.ListRuns(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "pipeline": row.Pipeline, "revision_id": row.RevisionID,
			"commit_sha": row.CommitSHA, "status": row.Status, "blocked_reason": row.BlockedReason, "created_at": row.CreatedAt, "finished_at": row.FinishedAt})
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"runs": items})
}

func (h Handler) Run(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := h.Store.Run(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) Cancel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) || !validID(w, r.PathValue("id")) {
		return
	}
	row, err := h.Store.Cancel(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.audit(r, acct.ID, "ci.run.cancelled", map[string]any{"run_id": row.ID})
	httpkit.WriteJSON(w, http.StatusOK, row)
}

func (h Handler) audit(r *http.Request, account, kind string, data map[string]any) {
	if h.Audit != nil {
		h.Audit(r.Context(), kind, &account, data)
	}
}

func (h Handler) Events(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) || !validID(w, r.PathValue("id")) {
		return
	}
	rows, err := h.Store.Events(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"events": rows})
}

func writeError(w http.ResponseWriter, err error) {
	var quota *ci.LimitError
	switch {
	case errors.Is(err, ci.ErrDispatchDisabled):
		httpkit.NotFound(w, "CI execution admission is not available")
	case errors.Is(err, ci.ErrLogCursor):
		api.WriteProblem(w, api.ErrValidation("invalid CI log offset or page size"))
	case errors.Is(err, ci.ErrNotFound):
		httpkit.NotFound(w, "CI resource not found")
	case errors.Is(err, ci.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "ci_identity_conflict", "CI request conflict", "CI revision, profile, repository binding, job template or request identity conflicts with this request"))
	case errors.Is(err, ci.ErrAutomationRetryConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "ci_automation_retry_conflict", "CI automation retry conflict", "retry requires a never-admitted exhausted source, unchanged active policy and profile, current retry generation and a unique request identity"))
	case errors.Is(err, ci.ErrCheckRetryConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "ci_check_retry_conflict", "CI Check retry conflict", "retry requires an exhausted delivery, the current retry generation and a unique request identity; active publication or pending recovery prevents retry"))
	case errors.Is(err, ci.ErrExecutionArchived):
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "ci_execution_archived", "CI execution archived", "execution detail was compacted; its identity remains reserved and its receipt is available in CI archives"))
	case errors.Is(err, ci.ErrArchiveUnsafe):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "ci_archive_not_ready", "CI execution cannot be archived yet", "archive requires a terminal execution, confirmed task cleanup, retired payloads and settled Check delivery with no pending retry"))
	case errors.Is(err, ci.ErrRerunNotReady):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "ci_rerun_not_ready", "CI execution cannot be rerun yet", "rerun requires a terminal execution, confirmed native stop, collected output and no live runtime transfers"))
	case errors.Is(err, ci.ErrGitHubAccess):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "ci_github_access_denied", "CI repository unavailable", "the repository is not available to this account and installation"))
	case errors.Is(err, ci.ErrGitHubUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "ci_github_unavailable", "CI GitHub unavailable", "GitHub repository access could not be verified"))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "ci_metadata_limit", "Internal CI limit reached", "the bounded internal CI "+quota.Resource+" allowance is full").WithLimit(int64(quota.Limit), int64(quota.Observed)).WithDocs("/docs/ops/internal-ci"))
	default:
		api.WriteProblem(w, api.ErrInternal("CI storage unavailable"))
	}
}
