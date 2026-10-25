package apidserver

// ADR-831 amendment 13 on the dashboard edge-rules page: the WAF
// false-positive review, with a button that excludes a group's CRS rule.

import (
	"cmp"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardEdgeWAFReviewLabel words an assessment for the page.
func dashboardEdgeWAFReviewLabel(assessment string) string {
	switch assessment {
	case api.EdgeWAFReviewLikelyFalsePositive:
		return "likely false positive"
	case api.EdgeWAFReviewLikelyAttack:
		return "likely attack"
	case api.EdgeWAFReviewExcluded:
		return "excluded"
	}
	return "review"
}

// loadDashboardEdgeWAFReview runs the review over the app's last 7 days (the
// plan window caps it) when the app has a kind=waf rule. A read failure
// shows an inline message.
func (s *server) loadDashboardEdgeWAFReview(r *http.Request, log *slog.Logger, acct state.Account, app state.App, rules []state.EdgeRule) dashboard.EdgeWAFReviewPageData {
	var data dashboard.EdgeWAFReviewPageData
	for _, rule := range rules {
		data.Show = data.Show || rule.Kind == state.EdgeRuleKindWAF
	}
	store, ok := s.store.(state.EdgeRuleEventStore)
	if !data.Show || !ok {
		return data
	}
	query := edgerules.DefaultWAFReviewQuery(acct.Plan, app.ID, time.Now().UTC())
	review, err := edgerules.ReviewWAFDetections(r.Context(), store, query, rules)
	if err != nil {
		log.Warn("dashboard edge rules: waf review", "app_id", app.ID, "err", err)
		data.ErrorMessage = "The WAF review is temporarily unavailable."
		return data
	}
	data.SinceLabel, data.Scanned, data.Truncated = review.Since.Format("2006-01-02 15:04 MST"), review.Scanned, review.Truncated
	for _, g := range review.Groups {
		example := ""
		if len(g.RequestIDs) > 0 {
			example = g.RequestIDs[0]
		}
		data.Groups = append(data.Groups, dashboard.EdgeWAFReviewView{
			RuleID: g.RuleID, RuleLabel: cmp.Or(g.RuleName, "waf rule"), RuleMode: g.RuleMode, CRSRuleID: g.CRSRuleID,
			Category: g.Category, Path: g.Path, Events: g.Events, Clients: g.Clients,
			Assessment: g.Assessment, AssessmentLabel: dashboardEdgeWAFReviewLabel(g.Assessment), Reason: g.Reason,
			ExampleRequestID: example, Excluded: g.Excluded,
		})
	}
	return data
}

// dashboardExcludeEdgeWAFRule adds one CRS rule to a kind=waf rule's
// exclusions through the JSON handler, so validation, versioning and audit
// are the API's.
func (s *server) dashboardExcludeEdgeWAFRule(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	if !s.verifyDashboardEdgeRulesCSRF(w, r, acct.ID) {
		return
	}
	slug, id := r.PathValue("slug"), r.PathValue("id")
	page := "/dashboard/apps/" + url.PathEscape(slug) + "/edge-rules"
	crs, err := strconv.Atoi(r.PostFormValue("crs_rule_id"))
	if err != nil {
		http.Redirect(w, r, page+"?action=error", http.StatusSeeOther)
		return
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	rule, err := s.store.GetEdgeRuleByID(r.Context(), id)
	if err != nil || rule.AccountID != acct.ID || rule.AppID != app.ID {
		http.NotFound(w, r)
		return
	}
	resp := s.forwardDashboardEdgeRuleJSON(r, acct, http.MethodPost, "/v1/edge-rules/"+url.PathEscape(id)+"/waf-exclusions", id,
		api.EdgeWAFExclusionsRequest{Add: []int{crs}}, s.edgeRules.UpdateEdgeWAFExclusions)
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	http.Redirect(w, r, page+"?action=waf_excluded#waf-review", http.StatusSeeOther)
}
