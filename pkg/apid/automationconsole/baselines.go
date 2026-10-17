// adr: 1303
package automationconsole

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func (s *Service) inspectBaseline(r *http.Request, scope Scope, data *dashboard.AutomationConsoleData) {
	if data.Version == 0 {
		return
	}
	s.inspectBaselineCatalog(r, scope, data)
	form := data.Baseline
	if form == nil {
		name := r.URL.Query().Get("baseline_name")
		if name == "" {
			name = "production"
		}
		form = &dashboard.AutomationBaselineForm{Name: name}
		data.Baseline = form
	}
	if !api.ValidAutomationLiveBaselineName(form.Name) {
		form.Error = "Use a baseline name with lowercase letters, digits and hyphens (up to 63 characters)."
		return
	}
	response := s.Host.Request(r, scope, Operation{Kind: "baseline-get", Name: data.Name, BaselineName: form.Name})
	var head api.AutomationLiveBaseline
	if response.Status == http.StatusGone {
		form.Ready = false
		form.Head = nil
		form.Command = ""
		var history api.AutomationLiveBaselineHistory
		if read(s.Host.Request(r, scope, Operation{Kind: "baseline-history", Name: data.Name, BaselineName: form.Name}), &history) && history.Retirement != nil && history.Retirement.AppID == scope.AppID && history.Retirement.Name == data.Name && history.Retirement.BaselineName == form.Name {
			valid := len(history.Promotions) > 0 && len(history.Promotions) <= api.AutomationLiveBaselineHistoryLimit
			for _, receipt := range history.Promotions {
				valid = valid && baselineInScope(receipt, scope, data.Name, form.Name)
			}
			if valid {
				form.History = &history
			}
		}
		form.Error = "This baseline is permanently retired. Choose an active name for CI. Its retained history remains available."
		return
	}
	if response.Status == http.StatusNotFound {
		// Only a definite absence permits the initial expected revision of zero.
		form.Ready = true
	} else if !read(response, &head) || !baselineInScope(head, scope, data.Name, form.Name) {
		form.Error = "The current baseline is unavailable. Reload before preparing a promotion."
		return
	} else {
		form.Head = &head
		if form.RetirementID == "" {
			form.RetirementID = uuid.NewString()
		}
		form.Revision = head.Revision
		form.Ready = true
		if form.Policy == "" {
			form.Policy = string(head.Policy)
		}
		var history api.AutomationLiveBaselineHistory
		if read(s.Host.Request(r, scope, Operation{Kind: "baseline-history", Name: data.Name, BaselineName: form.Name}), &history) {
			valid := len(history.Promotions) <= api.AutomationLiveBaselineHistoryLimit
			for _, receipt := range history.Promotions {
				valid = valid && baselineInScope(receipt, scope, data.Name, form.Name)
			}
			if valid {
				form.History = &history
			} else {
				form.Error = "Baseline history is unavailable."
			}
		} else {
			form.Error = "Baseline history is unavailable."
		}
	}
	if form.Policy == "" && data.LiveSuite != nil {
		names := make([]string, 0, len(data.LiveSuite.Cases))
		for _, entry := range data.LiveSuite.Cases {
			names = append(names, entry.Name)
		}
		age := int64(time.Hour / time.Second)
		form.Policy = pretty(automationgate.CoveragePolicy{Version: 2, RequiredCases: names, MaxCandidateAgeSeconds: &age})
	}
	if form.Head != nil && data.LiveSuite != nil && form.Head.Suite.ID != data.LiveSuite.ID {
		q := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
		suite := data.LiveSuite
		form.Command = fmt.Sprintf("gregale --non-interactive automations live gate --app %s --name %s --baseline-name %s --candidate %s --expected-version %d --expected-published-version %d --expected-deployment-id %s --json", q(scope.Slug), q(data.Name), q(form.Name), q(suite.ID), suite.Version, suite.PublishedVersion, q(suite.DeploymentID))
	}
}

func baselineInScope(receipt api.AutomationLiveBaseline, scope Scope, name, baseline string) bool {
	return receipt.AppID == scope.AppID && receipt.Name == name && receipt.BaselineName == baseline && receipt.Revision > 0
}

func (s *Service) baselineAction(w http.ResponseWriter, r *http.Request, scope Scope, data dashboard.AutomationConsoleData) {
	ctx, cancel := context.WithTimeout(r.Context(), api.WorkflowAutomationHealthReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	form := &dashboard.AutomationBaselineForm{Name: r.PostFormValue("baseline_name"), Policy: r.PostFormValue("baseline_policy"), PromotionID: r.PostFormValue("promotion_id")}
	data.Baseline = form
	data.LiveSuiteID = r.PostFormValue("suite_id")
	request, err := baselineIntent(r, data.Name)
	if err != nil {
		s.invalid(w, r, scope, data, "Choose a retained suite, a valid version 2 policy, and reload the target and baseline revision before previewing.")
		return
	}
	form.Request = &request
	// These forms are separate from the draft editor. Reload saved editor data
	// rather than substituting empty or browser-supplied definitions on response.
	if failure := s.selectAutomation(r, scope, &data, data.Name); failure != nil {
		data.Error = "The saved automation is unavailable. No promotion was attempted."
		s.render(w, r, scope, data, failure.Status)
		return
	}
	if r.PathValue("action") == "promote-baseline" {
		id, err := uuid.Parse(form.PromotionID)
		if err != nil || id == uuid.Nil || r.PostFormValue("confirm_baseline") != "yes" {
			s.invalid(w, r, scope, data, "Preview the promotion and explicitly confirm replacing the named baseline.")
			return
		}
		response := s.Host.Request(r, scope, Operation{Kind: "baseline-promote", Name: data.Name, BaselineName: form.Name, PromotionID: id.String(), Body: request})
		var receipt api.AutomationLiveBaseline
		if !read(response, &receipt) || !baselineInScope(receipt, scope, data.Name, form.Name) || receipt.PromotionID != id.String() || receipt.Suite.ID != request.SuiteID || receipt.Revision != request.ExpectedRevision+1 || receipt.Suite.Version != request.ExpectedVersion || receipt.Suite.PublishedVersion != request.ExpectedPublishedVersion || receipt.Suite.DeploymentID != request.ExpectedDeploymentID || !sameBaselinePolicy(receipt.Policy, request.Policy) {
			form.Error = "Promotion was not confirmed. Reload the current baseline after a conflict. For an uncertain response, retry the same promotion ID and unchanged intent."
			// Keep the exact original request and ID available for a deliberate retry.
			status := response.Status
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			s.render(w, r, scope, data, status)
			return
		}
		data.Message = fmt.Sprintf("Promotion recorded as baseline revision %d (%s). The current head is shown below; an earlier retry does not move it.", receipt.Revision, receipt.PromotionID)
		form.Request = nil
		form.PromotionID = ""
		s.render(w, r, scope, data, http.StatusOK)
		return
	}
	var comparison api.AutomationLiveSuiteComparison
	if !read(s.Host.Request(r, scope, Operation{Kind: "live-suite-compare", Name: data.Name, RunID: request.SuiteID, BaselineID: request.SuiteID}), &comparison) {
		s.invalid(w, r, scope, data, "The selected suite evidence is unavailable. No promotion was attempted.")
		return
	}
	policy, _ := automationgate.ParseCoveragePolicy(request.Policy)
	result, err := automationgate.EvaluatePromotionAt(automationgate.Selection{Name: data.Name, BaselineID: request.SuiteID, CandidateID: request.SuiteID, Coverage: &policy, Expected: automationgate.Target{Version: request.ExpectedVersion, PublishedVersion: request.ExpectedPublishedVersion, DeploymentID: request.ExpectedDeploymentID}}, comparison, time.Now().UTC())
	if err != nil {
		s.invalid(w, r, scope, data, "The recorded evidence could not be evaluated. No promotion was attempted.")
		return
	}
	form.Preview = &result
	form.PromotionID = uuid.NewString()
	s.render(w, r, scope, data, http.StatusOK)
}

func baselineIntent(r *http.Request, name string) (api.PromoteAutomationLiveBaselineRequest, error) {
	var request api.PromoteAutomationLiveBaselineRequest
	request.SuiteID = r.PostFormValue("suite_id")
	request.ExpectedDeploymentID = r.PostFormValue("expected_deployment_id")
	revision, e1 := strconv.ParseInt(r.PostFormValue("baseline_revision"), 10, 64)
	version, e2 := strconv.ParseInt(r.PostFormValue("expected_version"), 10, 64)
	published, e3 := strconv.ParseInt(r.PostFormValue("expected_published_version"), 10, 64)
	request.ExpectedRevision, request.ExpectedVersion, request.ExpectedPublishedVersion = revision, version, published
	policy, err := automationgate.ParseCoveragePolicy([]byte(r.PostFormValue("baseline_policy")))
	if err != nil || policy.Version != 2 || e1 != nil || e2 != nil || e3 != nil || revision < 0 || revision == int64(^uint64(0)>>1) || !api.ValidAutomationLiveBaselineName(r.PostFormValue("baseline_name")) {
		return request, fmt.Errorf("invalid promotion intent")
	}
	selection := automationgate.Selection{Name: name, CandidateID: request.SuiteID, Coverage: &policy, Expected: automationgate.Target{Version: version, PublishedVersion: published, DeploymentID: request.ExpectedDeploymentID}}
	if err := selection.ValidateCandidate(); err != nil {
		return request, err
	}

	// Stores return canonical UUIDs even when an existing target uses the compact
	// spelling. Keep response checks and retries bound to the same UUID identity.
	request.SuiteID = uuid.MustParse(request.SuiteID).String()
	request.ExpectedDeploymentID = uuid.MustParse(request.ExpectedDeploymentID).String()
	request.Policy, err = json.Marshal(policy)
	if int64(len(request.Policy)) > api.AutomationLiveGatePolicyMaxBytes {
		return request, fmt.Errorf("policy exceeds canonical size limit")
	}
	return request, err
}

func sameBaselinePolicy(a, b []byte) bool {
	left, e1 := automationgate.ParseCoveragePolicy(a)
	right, e2 := automationgate.ParseCoveragePolicy(b)
	return e1 == nil && e2 == nil && left.Hash() == right.Hash()
}
