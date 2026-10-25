package apidserver

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) WithProjectReleaseSecretsEnabled(enabled bool) *server {
	s.projectReleaseSecretsEnabled = enabled
	return s
}
func (s *server) validateReleaseSecretRequest(r *http.Request, req *scanPlanRequest) *api.Problem {
	var config api.ProjectReleaseConfigRequest
	_ = json.Unmarshal(req.ReleaseConfig, &config)
	if len(config.Secrets) == 0 {
		return nil
	}
	if !s.projectReleaseSecretsEnabled {
		return api.ErrCapacity("atomic project secret releases are disabled pending native qualification")
	}
	p, ok := principalFrom(r)
	if !ok || !principalHasScope(p, api.ScopesSecretsWriteSurface) {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope", "release secret changes require secrets:write or admin")
	}
	return nil
}
func (s *server) stageProjectReleaseSecrets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.projectReleaseSecretsEnabled {
		api.WriteProblem(w, api.ErrCapacity("atomic project secret releases are disabled pending native qualification"))
		return
	}
	var req api.ProjectReleaseSecretStageRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProjectReleaseSecretStageMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || !api.ValidDeploymentAttemptID(req.RequestID) {
		api.WriteProblem(w, api.ErrValidation("require a request_id and bounded workload secret snapshots"))
		return
	}
	store, ok := s.store.(state.ProjectReleaseSecretsStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("release secret staging unavailable"))
		return
	}
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, r.PathValue("slug"))
	if err != nil {
		s.notFound(w, "project not found")
		return
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("project workloads unavailable"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	recipient := runtimeSecretRecipient()
	if recipient == nil {
		api.WriteProblem(w, api.ErrCapacity("release secret encryption unavailable"))
		return
	}
	rows, hash, err := deploystaging.SealProjectReleaseSecrets(acct.ID, apps, req, limits, hostHMACKey(), func(key, value string) ([]byte, string, string, error) {
		return deploystaging.EncryptProjectReleaseSecret(recipient, runtimeSecretBinding(acct.ID), limits, hostHMACKey(), key, value)
	})
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	response, err := store.StageProjectReleaseSecrets(r.Context(), acct.ID, project.ID, req.RequestID, hash, rows, req.Workloads)
	if s.writeProjectControlError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, response)
}
