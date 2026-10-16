package apidserver

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) resolveProjectMigrationContract(w http.ResponseWriter, r *http.Request, account state.Account) {
	attempt := r.PathValue("attempt_id")
	var req api.ProjectMigrationRecoveryRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProjectReleaseControlMaxBytes))
	decoder.DisallowUnknownFields()
	if !api.ValidDeploymentAttemptID(attempt) || decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || !req.Valid() || req.ReplacementAttemptID == attempt {
		api.WriteProblem(w, api.ErrValidation("require inspected cleanup task/generation, a different replacement attempt, database/execution attestations, evidence hash and request ID"))
		return
	}
	store, ok := s.store.(deploystaging.MigrationRecoveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("migration recovery is unavailable"))
		return
	}
	_, err := deploystaging.ResolveProjectMigrationContract(r.Context(), store, account.ID, attempt, req, s.durable.CheckProjectValidatorMembers, s.probeProjectRelease)
	if s.writeProjectControlError(w, err) {
		return
	}
	s.getProjectSubmission(w, r, account)
}
