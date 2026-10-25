package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) controlProjectRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.PathValue("action") == "resolve-contract" {
		s.resolveProjectMigrationContract(w, r, acct)
		return
	}
	attempt, action := r.PathValue("attempt_id"), r.PathValue("action")
	var req api.ProjectReleaseControlRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProjectReleaseControlMaxBytes))
	decoder.DisallowUnknownFields()
	if !api.ValidDeploymentAttemptID(attempt) || (action != "abort" && action != "recover" && action != "contract") || decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || !api.ValidDeploymentAttemptID(req.RequestID) || req.ExpectedGeneration == 0 {
		api.WriteProblem(w, api.ErrValidation("require a progressive attempt, abort/recover/contract action, request_id and expected_generation"))
		return
	}
	store, ok := s.store.(state.ProjectCanaryControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("progressive deployment controls are unavailable"))
		return
	}
	var err error
	if action == "contract" {
		if !s.projectMigrationContractsEnabled {
			api.WriteProblem(w, api.ErrCapacity("schema contract admission is disabled"))
			return
		}
		contractStore, ok := s.store.(deploystaging.MigrationContractStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("schema contract admission is unavailable"))
			return
		}
		_, err = deploystaging.AdmitProjectMigrationContract(r.Context(), contractStore, acct.ID, attempt, req, s.durable.CheckProjectValidatorMembers, s.probeProjectRelease)
	} else {
		_, err = store.ControlProjectCanary(r.Context(), acct.ID, attempt, action, req)
	}

	if s.writeProjectControlError(w, err) {
		return
	}
	s.getProjectSubmission(w, r, acct)
}

func (s *server) WithProjectMigrationContractsEnabled(enabled bool) *server {
	s.projectMigrationContractsEnabled = enabled
	return s
}

func (s *server) writeProjectControlError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, state.ErrProjectReleaseBaselineExpired) {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "project_release_baseline_expired", "Rollback baseline expired", "The retained baseline has expired. Deploy a compatible replacement; this attempt cannot restore it."))
		return true
	}
	var blocked *state.MigrationRecoveryBlockedError
	if errors.As(err, &blocked) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "migration_recovery_blocked", "Migration recovery blocked", blocked.Reason))
		return true
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "progressive deployment not found")
		return true
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "project_release_control_conflict", "Deployment control conflicted", "Inspect the current deployment generation and available actions; newer production intent will not be overwritten."))
		return true
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("deployment does not support this progressive control request"))
		return true
	}
	if err != nil {
		s.log.Warn("apid: progressive control failed", "err", err)
		api.WriteProblem(w, api.ErrCapacity("progressive control could not be recorded"))
		return true
	}
	return false
}
