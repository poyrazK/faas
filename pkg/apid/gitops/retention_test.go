package gitops

import (
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRecoveryRetentionProblemsExposeBudgetWithoutPrivateErrors(t *testing.T) {
	observed := api.EnvironmentRecoveryAccountMaxBytes + 1
	p := RetentionProblem(&state.EnvironmentRecoveryQuotaError{Observed: observed})
	if p == nil || p.Status != http.StatusForbidden || p.Limit == nil || *p.Limit != api.EnvironmentRecoveryAccountMaxBytes || p.Observed == nil || *p.Observed != observed {
		t.Fatal(p)
	}
	if p := RetentionProblem(state.ErrLayerArtifactRetired); p == nil || p.Status != http.StatusConflict {
		t.Fatal(p)
	}
	if RetentionProblem(errors.New("PRIVATE_STORAGE_KEY")) != nil {
		t.Fatal("private storage failure exposed")
	}
}
