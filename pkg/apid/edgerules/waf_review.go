package edgerules

// ADR-831 amendment 13: the WAF false-positive review and one-step
// exclusions.

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// wafReviewDefaultSince is the review window when the query names none,
// still capped by the plan's event window.
const wafReviewDefaultSince = "7d"

// GET /v1/apps/{slug}/edge-rules/waf-review?rule=&since=
func (s *Service) GetEdgeWAFReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.EdgeRuleEventStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("edge-rule events are unavailable"))
		return
	}
	query, prob := WAFReviewQuery(r, acct.Plan, app.ID, time.Now().UTC())
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	rules, err := s.Host.Store().ListEdgeRulesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge rules"))
		return
	}
	review, err := ReviewWAFDetections(r.Context(), store, query, rules)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read edge-rule events"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, review)
}

// ReviewWAFDetections reads the detections query selects and reviews them
// against the app's rules.
func ReviewWAFDetections(ctx context.Context, store state.EdgeRuleEventStore, query state.EdgeRuleEventQuery, rules []state.EdgeRule) (api.EdgeWAFReviewResponse, error) {
	events, truncated, err := wafReviewEvents(ctx, store, query)
	if err != nil {
		return api.EdgeWAFReviewResponse{}, err
	}
	return WAFReview(query.Since, events, rules, truncated), nil
}

// WAFReviewQuery validates the review's query string (rule, since) against
// the plan window.
func WAFReviewQuery(r *http.Request, plan api.Plan, appID string, now time.Time) (state.EdgeRuleEventQuery, *api.Problem) {
	q := r.URL.Query()
	return wafReviewQuery(plan, appID, q.Get("rule"), cmp.Or(q.Get("since"), wafReviewDefaultSince), now)
}

// DefaultWAFReviewQuery is the review of every rule over the default window.
func DefaultWAFReviewQuery(plan api.Plan, appID string, now time.Time) state.EdgeRuleEventQuery {
	q, _ := wafReviewQuery(plan, appID, "", wafReviewDefaultSince, now)
	return q
}

// wafReviewQuery builds the read; Outcome is set per read by
// wafReviewEvents.
func wafReviewQuery(plan api.Plan, appID, ruleID, rawSince string, now time.Time) (state.EdgeRuleEventQuery, *api.Problem) {
	out := state.EdgeRuleEventQuery{AppID: appID, RuleID: ruleID, Limit: api.EdgeWAFReviewMaxEvents + 1}
	if out.RuleID != "" {
		if _, err := uuid.Parse(out.RuleID); err != nil {
			return out, api.ErrValidation("rule must be an edge rule id")
		}
	}
	since, err := parseEdgeRuleEventsSince(rawSince)
	if err != nil {
		return out, api.ErrValidation(err.Error())
	}
	window := time.Duration(api.MustLimitsFor(plan).EdgeRuleEventsWindowHours) * time.Hour
	out.Since = now.Add(-min(since, window))
	return out, nil
}

// wafReviewEvents reads the newest EdgeWAFReviewMaxEvents detections of
// each outcome and reports whether any outcome had more.
func wafReviewEvents(ctx context.Context, store state.EdgeRuleEventStore, q state.EdgeRuleEventQuery) ([]state.EdgeRuleEvent, bool, error) {
	var all []state.EdgeRuleEvent
	truncated := false
	for _, outcome := range []string{state.EdgeRuleEventDetected, state.EdgeRuleEventWarned, state.EdgeRuleEventBlocked} {
		q.Outcome = outcome
		events, err := store.ListEdgeRuleEvents(ctx, q)
		if err != nil {
			return nil, false, fmt.Errorf("edgerules: read %s detections: %w", outcome, err)
		}
		if len(events) > api.EdgeWAFReviewMaxEvents {
			events, truncated = events[:api.EdgeWAFReviewMaxEvents], true
		}
		all = append(all, events...)
	}
	return all, truncated, nil
}

type wafReviewKey struct {
	rule string
	crs  int
	path string
}

type wafReviewAcc struct {
	group   api.EdgeWAFReviewGroup
	clients map[string]int
	scanner int
}

// WAFReview groups detections by edge rule, CRS rule and review path and
// assesses each group. It sorts events newest first, in place.
func WAFReview(since time.Time, events []state.EdgeRuleEvent, rules []state.EdgeRule, truncated bool) api.EdgeWAFReviewResponse {
	slices.SortFunc(events, func(a, b state.EdgeRuleEvent) int {
		return cmp.Or(b.OccurredAt.Compare(a.OccurredAt), cmp.Compare(b.ID, a.ID))
	})
	byID := make(map[string]state.EdgeRule, len(rules))
	for _, rule := range rules {
		byID[rule.ID] = rule
	}
	breadth := clientPathBreadth(events)
	groups := map[wafReviewKey]*wafReviewAcc{}
	for _, e := range events {
		path := reviewPath(e.Path)
		scanner := e.ClientIP != "" && breadth[e.ClientIP] >= api.EdgeWAFReviewScannerPaths
		for _, crs := range slices.Compact(slices.Sorted(slices.Values(e.WAFRuleIDs))) {
			k := wafReviewKey{rule: e.RuleID, crs: crs, path: path}
			acc := groups[k]
			if acc == nil {
				acc = newWAFReviewAcc(k, byID[e.RuleID])
				groups[k] = acc
			}
			acc.add(e, scanner)
		}
	}
	out := api.EdgeWAFReviewResponse{Since: since, Scanned: len(events), Truncated: truncated, Groups: make([]api.EdgeWAFReviewGroup, 0, len(groups))}
	for _, acc := range groups {
		out.Groups = append(out.Groups, acc.finish())
	}
	slices.SortFunc(out.Groups, func(a, b api.EdgeWAFReviewGroup) int {
		return cmp.Or(cmp.Compare(b.Events, a.Events), strings.Compare(a.RuleID, b.RuleID),
			cmp.Compare(a.CRSRuleID, b.CRSRuleID), strings.Compare(a.Path, b.Path))
	})
	if len(out.Groups) > api.EdgeWAFReviewMaxGroups {
		out.Groups = out.Groups[:api.EdgeWAFReviewMaxGroups]
	}
	return out
}

// clientPathBreadth counts the distinct review paths each client was
// flagged on.
func clientPathBreadth(events []state.EdgeRuleEvent) map[string]int {
	paths := map[string]map[string]bool{}
	for _, e := range events {
		if e.ClientIP == "" {
			continue
		}
		if paths[e.ClientIP] == nil {
			paths[e.ClientIP] = map[string]bool{}
		}
		paths[e.ClientIP][reviewPath(e.Path)] = true
	}
	out := make(map[string]int, len(paths))
	for ip, set := range paths {
		out[ip] = len(set)
	}
	return out
}

func newWAFReviewAcc(k wafReviewKey, rule state.EdgeRule) *wafReviewAcc {
	g := api.EdgeWAFReviewGroup{
		RuleID: k.rule, RuleName: rule.Name, CRSRuleID: k.crs, Path: k.path,
		Category: api.EdgeWAFRuleCategory(k.crs), KnownFalsePositive: api.EdgeWAFKnownFalsePositive(k.crs),
		RequestIDs: []string{},
	}
	if waf := rule.Action.WAF; waf != nil {
		g.RuleMode = waf.Mode
		g.Excluded = slices.Contains(waf.ExcludeRuleIDs, k.crs)
	}
	return &wafReviewAcc{group: g, clients: map[string]int{}}
}

// add counts one detection; events arrive newest first.
func (a *wafReviewAcc) add(e state.EdgeRuleEvent, scanner bool) {
	g := &a.group
	g.Events++
	switch e.Outcome {
	case state.EdgeRuleEventWarned:
		g.Warned++
	case state.EdgeRuleEventBlocked:
		g.Blocked++
	default:
		g.Detected++
	}
	if g.LastSeen.IsZero() {
		g.LastSeen = e.OccurredAt
	}
	g.FirstSeen = e.OccurredAt
	if e.RequestID != "" && len(g.RequestIDs) < api.EdgeWAFReviewExamples {
		g.RequestIDs = append(g.RequestIDs, e.RequestID)
	}
	if e.ClientIP != "" {
		a.clients[e.ClientIP]++
	}
	if scanner {
		a.scanner++
	}
}

func (a *wafReviewAcc) finish() api.EdgeWAFReviewGroup {
	g := a.group
	g.Clients = len(a.clients)
	top := 0
	for _, n := range a.clients {
		top = max(top, n)
	}
	g.TopClientPercent = top * 100 / g.Events
	g.ScannerPercent = a.scanner * 100 / g.Events
	g.Assessment, g.Reason = assessWAFReviewGroup(g)
	return g
}

// assessWAFReviewGroup labels a group. Scanning beats the catalog: a
// scanner that trips a rule legitimate traffic also trips is still an
// attack.
func assessWAFReviewGroup(g api.EdgeWAFReviewGroup) (string, string) {
	switch {
	case g.Excluded:
		return api.EdgeWAFReviewExcluded, fmt.Sprintf("the rule excludes CRS %d now; these detections came before the exclusion", g.CRSRuleID)
	case g.ScannerPercent >= 50:
		return api.EdgeWAFReviewLikelyAttack, fmt.Sprintf("%d%% of these detections came from clients flagged on %d or more paths, as scanners are",
			g.ScannerPercent, api.EdgeWAFReviewScannerPaths)
	case g.KnownFalsePositive != "":
		return api.EdgeWAFReviewLikelyFalsePositive, "a documented false positive: " + g.KnownFalsePositive
	case g.Clients >= api.EdgeWAFReviewMinClients && g.TopClientPercent <= 50:
		return api.EdgeWAFReviewLikelyFalsePositive, fmt.Sprintf("%d clients were flagged on this path and none sent most of the detections; "+
			"attacks usually come from a few clients across many paths", g.Clients)
	}
	return api.EdgeWAFReviewUndecided, "too few clients to tell; open the example requests"
}

// reviewPath reads id-like path segments as {id} so one endpoint's
// detections group together.
func reviewPath(path string) string {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if idLikeSegment(seg) {
			segs[i] = "{id}"
		}
	}
	return strings.Join(segs, "/")
}

// idLikeSegment reports a number, a UUID, hex of 16 or more characters with
// a digit, or a token of 20 or more letters and digits that has both.
// Segments with other characters, such as file names, are kept.
func idLikeSegment(seg string) bool {
	if seg == "" {
		return false
	}
	if len(seg) == 36 {
		if _, err := uuid.Parse(seg); err == nil {
			return true
		}
	}
	digits, letters, hex := 0, 0, true
	for _, c := range seg {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			letters++
		case c >= 'g' && c <= 'z', c >= 'G' && c <= 'Z':
			letters++
			hex = false
		case c == '-' || c == '_':
			hex = false
		default:
			return false
		}
	}
	switch {
	case digits == len(seg):
		return true
	case hex && len(seg) >= 16 && digits > 0:
		return true
	default:
		return len(seg) >= 20 && digits > 0 && letters > 0
	}
}

// POST /v1/edge-rules/{id}/waf-exclusions
func (s *Service) UpdateEdgeWAFExclusions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	row, prob := s.ruleForUpdate(r.Context(), acct, r.PathValue("id"))
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var req api.EdgeWAFExclusionsRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	update, prob := WAFExclusionsUpdate(row, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	m, prob := s.updateRule(r.Context(), acct, row, update, func(ctx context.Context) (string, *api.Problem) {
		return s.ifMatchProblem(ctx, r, row.AppID)
	})
	s.writeRuleMutation(r.Context(), w, acct, m, prob, http.StatusOK)
}

// UpdateWAFExclusions applies req to one of the account's kind=waf rules
// through the same path as PATCH, without an If-Match precondition (the
// dashboard's exclude button).
func (s *Service) UpdateWAFExclusions(ctx context.Context, acct state.Account, ruleID string, req api.EdgeWAFExclusionsRequest) (api.EdgeRuleResponse, *api.Problem) {
	row, prob := s.ruleForUpdate(ctx, acct, ruleID)
	if prob != nil {
		return api.EdgeRuleResponse{}, prob
	}
	update, prob := WAFExclusionsUpdate(row, req)
	if prob != nil {
		return api.EdgeRuleResponse{}, prob
	}
	m, prob := s.updateRule(ctx, acct, row, update, nil)
	if prob != nil {
		return api.EdgeRuleResponse{}, prob
	}
	return s.RuleResponse(acct, m.row), nil
}

// WAFExclusionsUpdate is the update that applies req to a kind=waf rule's
// exclude_rule_ids, keeping the rest of its action (the penalty is kept by
// the update path).
func WAFExclusionsUpdate(row state.EdgeRule, req api.EdgeWAFExclusionsRequest) (api.UpdateEdgeRuleRequest, *api.Problem) {
	cur := row.Action.WAF
	if row.Kind != state.EdgeRuleKindWAF || cur == nil {
		return api.UpdateEdgeRuleRequest{}, api.ErrValidation("waf exclusions apply to kind=waf rules only")
	}
	if prob := req.Validate(); prob != nil {
		return api.UpdateEdgeRuleRequest{}, prob
	}
	a := api.EdgeRuleWAFAction{Mode: cur.Mode, ParanoiaLevel: cur.ParanoiaLevel, AnomalyThreshold: cur.AnomalyThreshold, InspectBodyBytes: cur.InspectBodyBytes}
	for _, id := range slices.Concat(cur.ExcludeRuleIDs, req.Add) {
		if !slices.Contains(req.Remove, id) {
			a.ExcludeRuleIDs = append(a.ExcludeRuleIDs, id)
		}
	}
	if prob := a.Validate(); prob != nil {
		return api.UpdateEdgeRuleRequest{}, prob
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return api.UpdateEdgeRuleRequest{}, api.ErrCapacity("could not encode the waf action")
	}
	action := json.RawMessage(raw)
	return api.UpdateEdgeRuleRequest{Action: &action}, nil
}
