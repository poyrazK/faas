package apidserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) projectApplySubmission(w http.ResponseWriter, r *http.Request, acct state.Account) {
	attempt := r.Header.Get(api.DeploymentAttemptHeader)
	if attempt == "" {
		s.idempotent(s.applyProject)(w, r, acct)
		return
	}
	if !api.ValidDeploymentAttemptID(attempt) {
		api.WriteProblem(w, api.ErrValidation("invalid project deployment attempt ID"))
		return
	}
	r = r.WithContext(deploystaging.ContextWithProjectSubmission(r.Context(), acct.ID, attempt))
	s.applyProject(w, r, acct)
}
func (s *server) beginProjectSubmission(ctx context.Context, req *scanPlanRequest) *api.Problem {
	tracker := deploystaging.ProjectSubmissionFromContext(ctx)
	if tracker == nil {
		return nil
	}
	store, ok := s.store.(state.ProjectSubmissionStore)
	if !ok {
		return api.ErrCapacity("project submission tracking is unavailable")
	}
	// Hash request semantics only; spool paths and credentials are never
	// persisted. Secret values enter only through a host-keyed MAC, so the
	// stored fingerprint cannot be brute-forced back to low-entropy values.
	secretsMAC, err := projectSubmissionSecretsMAC(req.Secrets, req.InitialSecrets)
	if err != nil {
		return api.ErrCapacity("project secrets cannot be fingerprinted on this server")
	}
	semantics := []any{req.SourceSHA256, req.ProjectSlug, req.RepoFullName, req.ProdBranch, req.InstallID, req.Only, req.Exclude, req.PersistExclude, req.NoTriggers, req.Environment, req.PlatformTenantRequired, secretsMAC, req.AppSlugs, req.Migration}
	if req.SafeRelease != nil {
		semantics = append(semantics, safeProjectReleaseID(req.SafeRelease))
	}
	if len(req.RetryTargets) > 0 {
		semantics = append(semantics, req.RetryTargets)
	}
	payload, _ := json.Marshal(semantics)
	sum := sha256.Sum256(payload)
	err = tracker.Begin(ctx, store, req.ProjectSlug, req.Environment, hex.EncodeToString(sum[:]))
	if errors.Is(err, state.ErrProjectSubmissionConflict) {
		return api.NewProblem(http.StatusConflict, "project_submission_mismatch", "Project submission intent changed", "This attempt belongs to different request bytes; use a new idempotency key for a new intent.")
	}
	if errors.Is(err, state.ErrProjectSubmissionExists) {
		return api.NewProblem(http.StatusConflict, "project_submission_exists", "Project submission already registered", "Inspect this attempt's receipt or resume the saved session; this request did not submit another build.")
	}
	if err != nil {
		s.log.Warn("apid: register project submission failed", "err", err)
		return api.ErrCapacity("project submission could not be recorded")
	}
	return nil
}

// projectSubmissionSecretsMAC binds apply-time secret bundles into a
// submission's intent without persisting their values or an unkeyed digest.
func projectSubmissionSecretsMAC(secrets map[string]string, initial api.ProjectInitialSecrets) (string, error) {
	if len(secrets) == 0 && len(initial) == 0 {
		return "", nil
	}
	key := hostHMACKeyBytes()
	if len(key) == 0 {
		return "", errors.New("host HMAC key unavailable")
	}
	// Map keys marshal in sorted order, so equal bundles produce equal MACs.
	payload, err := json.Marshal(struct {
		Secrets map[string]string         `json:"secrets,omitempty"`
		Initial api.ProjectInitialSecrets `json:"initial,omitempty"`
	}{secrets, initial})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *server) getProjectSubmission(w http.ResponseWriter, r *http.Request, acct state.Account) {
	attempt := r.PathValue("attempt_id")
	if !api.ValidDeploymentAttemptID(attempt) {
		api.WriteProblem(w, api.ErrValidation("invalid project deployment attempt ID"))
		return
	}
	store, ok := s.store.(state.ProjectSubmissionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project submission tracking is unavailable"))
		return
	}
	receipt, err := store.ProjectSubmission(r.Context(), acct.ID, attempt)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "project submission receipt not found")
		return
	}
	if err != nil {
		s.log.Warn("apid: read project submission failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("project submission receipt could not be read"))
		return
	}
	if safe := receipt.Apply.SafeRelease; safe != nil && safe.Verification != nil {
		safe.Verification.Actions = api.ProjectReleaseControlActions(safe)
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (s *server) finishProjectSubmission(ctx context.Context, tracker *deploystaging.ProjectSubmission) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := tracker.Finish(finishCtx); err != nil {
		s.log.Warn("apid: finish project submission failed", "err", err)
	}
}
