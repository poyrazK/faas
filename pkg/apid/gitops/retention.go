package gitops

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// RetentionProblem reports actionable policy failures without storage paths or values.
func RetentionProblem(err error) *api.Problem {
	var quota *state.EnvironmentRecoveryQuotaError
	if errors.As(err, &quota) {
		return api.NewProblem(http.StatusForbidden, "environment_gitops_recovery_retention_limit", "Recovery retention budget exceeded", "Reduce recovery_retention.max_bytes on this or another environment before retrying approval.").WithLimit(api.EnvironmentRecoveryAccountMaxBytes, quota.Observed).WithDocs("https://github.com/poyrazK/faas/blob/main/docs/environment-gitops.md")
	}
	if errors.Is(err, state.ErrLayerArtifactRetired) {
		return api.NewProblem(http.StatusConflict, "environment_gitops_recovery_artifact_unavailable", "Recovery artifact unavailable", "A selected historical artifact is missing or has entered garbage collection. Review a smaller retention window or release count; collected artifacts cannot be revived.")
	}
	return nil
}
