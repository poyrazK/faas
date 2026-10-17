package apidserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) promotionSecretOptions(target string, syncConfig bool, verify []bool) *api.Problem {
	if !syncConfig || len(verify) == 0 || !verify[0] || target != "production" || len(verify) > 1 && verify[1] || len(verify) > 2 && verify[2] {
		return api.ErrValidation("secret promotion requires --sync-config --verify to production without migrations or workers")
	}
	if !s.projectReleaseSecretsEnabled {
		return api.ErrCapacity("atomic project secret releases are disabled pending native qualification")
	}
	return nil
}

func validatePromotionSecretAccess(r *http.Request, refs map[string]map[string]string) *api.Problem {
	if len(refs) == 0 {
		return nil
	}
	if api.ValidatePromotionSecretReferences(refs) != nil {
		return api.ErrValidation("invalid promotion secret version references")
	}
	p, ok := principalFrom(r)
	if !ok || !principalHasScope(p, api.ScopesSecretsWriteSurface) {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Insufficient scope", "promotion secret changes require secrets:write or admin")
	}
	return nil
}

func (s *server) previewProjectEnvironmentPromotionSecrets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProjectEnvironmentPromotionSecretPreviewRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.ProjectReleaseConfigMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || len(req.Secrets) == 0 {
		api.WriteProblem(w, api.ErrValidation("require a bounded promotion request with secret version references"))
		return
	}
	if problem := validatePromotionSecretAccess(r, req.Secrets); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	plan, problem := s.buildProjectEnvironmentPromotionSecretPlan(r.Context(), acct, r.PathValue("slug"), req.FromEnvironment, r.PathValue("environment"), req.SyncConfig, req.Secrets, req.Verify)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, plan.Preview)
}

func promotionSecretDescriptor(plan projectEnvironmentPromotionPlan) *api.ProjectReleaseSecretConfiguration {
	if plan.ReleaseConfiguration == nil {
		return nil
	}
	return plan.ReleaseConfiguration.Secrets
}

func promotionSecretCaptureReason(err error) string {
	switch {
	case errors.Is(err, state.ErrReleaseConfigurationTransferUnsupported):
		return "secret promotion requires complete HTTP graphs with application-owned persistent production secrets and no managed credentials, secret references, sidecars, reload hooks, workers or migrations"
	case errors.Is(err, state.ErrInvalidArgument):
		return "production secret versions are missing, expired, reference another workload or a new key, or exceed destination limits; stage existing production keys and preview again"
	default:
		return ""
	}
}
