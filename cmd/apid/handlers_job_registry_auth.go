package main

import (
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
	if prob := req.Validate(); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if limits.RegistryCredentialMax == 0 {
		api.WriteProblem(w, api.ErrPlanJobRegistryCredentialsNotAllowed(acct.Plan))
		return
	}
	store, ok := s.jobRegistryCredentialStore(w)
	if !ok {
		return
	}
	count, exists, err := store.JobRegistryCredentialQuotaCheck(r.Context(), acct.ID, job.ID, host)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not check job registry credential quota"))
		return
	}
	if !exists && count >= limits.RegistryCredentialMax {
		api.WriteProblem(w, api.ErrPlanJobRegistryCredentialQuota(limits, count))
		return
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal"))
		return
	}
	ciphertext, err := secretbox.SealBytes(recipient, "registry_creds", []byte(req.Password), api.MaxRegistryPasswordBytes)
	if err != nil {
		if prob := api.AsProblem(err); prob != nil {
			api.WriteProblem(w, prob)
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not seal job registry credential"))
		}
		return
	}
	if err := store.UpsertJobRegistryCredential(r.Context(), acct.ID, job.ID, host, req.Username, ciphertext); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not persist job registry credential"))
		return
	}
	row, err := store.GetJobRegistryCredential(r.Context(), acct.ID, job.ID, host)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read back job registry credential"))
		return
	}
	s.log.Info("job registry credential set", "job", job.ID, "account", acct.ID, "registry", logsanitize.Field(host), "username", logsanitize.Field(req.Username))
	s.audit.Emit(r.Context(), "job_registry_credential.set", &acct.ID, map[string]any{"job_id": job.ID, "registry": host, "username": req.Username})
	writeJSON(w, http.StatusOK, toJobRegistryCredentialResponse(row))
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
