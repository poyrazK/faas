package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// toJobRegistryCredentialResponse projects metadata only. The sealed password
// is deliberately absent from the wire type.
func toJobRegistryCredentialResponse(row state.JobRegistryCredential) api.JobRegistryCredentialResponse {
	resp := api.JobRegistryCredentialResponse{
		Registry:  row.Registry,
		Username:  row.Username,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if row.LastUsedAt != nil {
		resp.LastUsedAt = row.LastUsedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

func (s *server) jobRegistryCredentialStore(w http.ResponseWriter) (state.JobRegistryCredentialStore, bool) {
	store, ok := s.store.(state.JobRegistryCredentialStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("job registry credentials are unavailable"))
		return nil, false
	}
	return store, true
}

func (s *server) resolveJobForRegistryCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Job, bool) {
	job, ok, err := s.resolveJob(r.Context(), r.PathValue("name"), acct)
	if err != nil {
		s.log.Error("resolve job registry credentials: job lookup failed", "account", acct.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not resolve job"))
		return state.Job{}, false
	}
	if !ok {
		s.notFound(w, "no such job")
		return state.Job{}, false
	}
	return job, true
}

func (s *server) listJobRegistryCredentials(w http.ResponseWriter, r *http.Request, acct state.Account) {
	job, ok := s.resolveJobForRegistryCredentials(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.jobRegistryCredentialStore(w)
	if !ok {
		return
	}
	rows, err := store.ListJobRegistryCredentials(r.Context(), acct.ID, job.ID)
	if err != nil {
		s.log.Error("list job registry credentials failed", "job", job.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list job registry credentials"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	out := make([]api.JobRegistryCredentialResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toJobRegistryCredentialResponse(row))
	}
	writeJSON(w, http.StatusOK, api.JobRegistryCredentialListResponse{
		Credentials: out,
		QuotaMax:    limits.RegistryCredentialMax,
		Count:       len(out),
	})
}

func (s *server) setJobRegistryCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	job, ok := s.resolveJobForRegistryCredentials(w, r, acct)
	if !ok {
		return
	}
	var req api.PutJobRegistryCredentialRequest
	if err := decodeJSONSized(r, &req, maxRegistryBodyBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	host, err := normalizeRegistryHost(req.Registry)
	if err != nil {
		api.WriteProblem(w, api.ErrInvalidRegistryHost(err))
		return
	}
	req.Registry = host
	row, prob := s.storeJobRegistryCredential(r.Context(), acct, job, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusOK, toJobRegistryCredentialResponse(row))
}

// storeJobRegistryCredential validates, seals and upserts one job registry
// credential. req.Registry must already be a normalized host. Image runs
// (ADR-1790) store their registry_auth on the runner job through it, so both
// paths share the plan gate, quota, seal and audit. The password never
// reaches a log line or the audit payload.
func (s *server) storeJobRegistryCredential(ctx context.Context, acct state.Account, job state.Job, req api.PutJobRegistryCredentialRequest) (state.JobRegistryCredential, *api.Problem) {
	if prob := req.Validate(); prob != nil {
		return state.JobRegistryCredential{}, prob
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.RegistryCredentialMax == 0 {
		return state.JobRegistryCredential{}, api.ErrPlanJobRegistryCredentialsNotAllowed(acct.Plan)
	}
	store, ok := s.store.(state.JobRegistryCredentialStore)
	if !ok {
		return state.JobRegistryCredential{}, api.ErrCapacity("job registry credentials are unavailable")
	}
	host := req.Registry
	count, exists, err := store.JobRegistryCredentialQuotaCheck(ctx, acct.ID, job.ID, host)
	if err != nil {
		return state.JobRegistryCredential{}, api.ErrCapacity("could not check job registry credential quota")
	}
	if !exists && count >= limits.RegistryCredentialMax {
		return state.JobRegistryCredential{}, api.ErrPlanJobRegistryCredentialQuota(limits, count)
	}
	recipient := hostSecretRecipient()
	if recipient == nil {
		return state.JobRegistryCredential{}, api.ErrCapacity("host age recipient not loaded — refusing to seal")
	}
	ciphertext, err := secretbox.SealBytes(recipient, "registry_creds", []byte(req.Password), api.MaxRegistryPasswordBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			return state.JobRegistryCredential{}, prob
		}
		return state.JobRegistryCredential{}, api.ErrCapacity("could not seal job registry credential")
	}
	if err := store.UpsertJobRegistryCredential(ctx, acct.ID, job.ID, host, req.Username, ciphertext); err != nil {
		return state.JobRegistryCredential{}, api.ErrCapacity("could not persist job registry credential")
	}
	row, err := store.GetJobRegistryCredential(ctx, acct.ID, job.ID, host)
	if err != nil {
		return state.JobRegistryCredential{}, api.ErrCapacity("could not read back job registry credential")
	}
	s.log.Info("job registry credential set", "job", job.ID, "account", acct.ID, "registry", logsanitize.Field(host), "username", logsanitize.Field(req.Username))
	s.audit.Emit(ctx, "job_registry_credential.set", &acct.ID, map[string]any{"job_id": job.ID, "registry": host, "username": req.Username})
	return row, nil
}

func (s *server) deleteJobRegistryCredential(w http.ResponseWriter, r *http.Request, acct state.Account) {
	job, ok := s.resolveJobForRegistryCredentials(w, r, acct)
	if !ok {
		return
	}
	host, err := normalizeRegistryHost(r.URL.Query().Get("registry"))
	if err != nil {
		api.WriteProblem(w, api.ErrInvalidRegistryHost(err))
		return
	}
	store, ok := s.jobRegistryCredentialStore(w)
	if !ok {
		return
	}
	if err := store.DeleteJobRegistryCredential(r.Context(), acct.ID, job.ID, host); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrJobRegistryCredentialNotFound(host, job.Name))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not delete job registry credential"))
		}
		return
	}
	s.log.Info("job registry credential deleted", "job", job.ID, "account", acct.ID, "registry", logsanitize.Field(host))
	s.audit.Emit(r.Context(), "job_registry_credential.deleted", &acct.ID, map[string]any{"job_id": job.ID, "registry": host})
	w.WriteHeader(http.StatusNoContent)
}
