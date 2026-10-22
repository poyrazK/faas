package apidserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/state"
)

// applyCachedProjectPlan consumes the source already reviewed by this account.
// It never re-fetches a mutable Git ref, and the normal apply path rechecks
// source identity, admission, selected workloads, configuration and ownership.
func (s *server) applyCachedProjectPlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.ApplyCachedProjectPlanRequest
	if err := decodeJSONSized(r, &request, defaultJSONBodyMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid project plan request"))
		return
	}
	token, err := decodePlanToken(request.PlanToken)
	if err != nil || token.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "preview_expired", "Review this project again", "The reviewed source is unavailable. Scan the repository again before deploying."))
		return
	}
	source, err := lookupPlanCache(token.Hash, acct.ID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusGone, "preview_expired", "Review this project again", "The reviewed source expired. Scan the repository again before deploying."))
		return
	}
	fields, err := cachedProjectApplyFields(token, request)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid initial project configuration"))
		return
	}
	replay, cleanup, err := projectsetup.Replay(r, source, fields)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not open the reviewed project source"))
		return
	}
	defer cleanup()
	query := replay.URL.Query()
	query.Set("plan_token", request.PlanToken)
	replay.URL.RawQuery = query.Encode()
	s.applyProject(w, replay, acct)
}

func cachedProjectApplyFields(token planTokenWire, request api.ApplyCachedProjectPlanRequest) (map[string]string, error) {
	slugs, err := json.Marshal(token.AppSlugs)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{
		"app_slugs":    string(slugs),
		"migration":    strconv.FormatBool(token.Migration),
		"project_slug": token.Slug, "repo_full_name": token.RepoFullName,
		"production_branch": token.ProductionBranch, "install_id": strconv.FormatInt(token.InstallID, 10),
		"environment": token.Environment, "no_triggers": strconv.FormatBool(token.NoTriggers),
		"only": strings.Join(request.Only, ","), "exclude": strings.Join(request.Exclude, ","),
		"approval_token": request.ApprovalToken,
	}
	if token.PlatformTenantRequired != nil {
		fields["platform_tenant_required"] = strconv.FormatBool(*token.PlatformTenantRequired)
	}
	if len(request.InitialSecrets) > 0 {
		body, err := json.Marshal(request.InitialSecrets)
		if err != nil {
			return nil, err
		}
		fields["initial_secrets"] = string(body)
	}
	return fields, nil
}
