package sales

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (s *Service) qualificationStore(w http.ResponseWriter, r *http.Request, acct state.Account) (referrals.QualificationStore, referrals.Scope, bool) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return nil, scope, false
	}
	store, ok := s.Store.(referrals.QualificationStore)
	if !ok {
		unavailable(w)
	}
	return store, scope, ok
}
func qualificationLead(p referrals.Prospect) map[string]any {
	return map[string]any{"id": p.ID, "name": p.Name, "email": p.Email, "company": p.Company, "stage": p.Stage, "note": p.Note, "owner_id": p.OwnerID, "owner_email": p.OwnerEmail, "version": p.Version}
}
func (s *Service) Qualification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.qualificationStore(w, r, acct)
	if !ok {
		return
	}
	lead := r.PathValue("lead")
	if !validID(lead) {
		invalid(w, "Choose a valid lead.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	profile, err := store.QualificationProfile(ctx, scope, lead)
	if err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.qualification_read", acct.ID, map[string]any{"lead_id": lead, "team_scope": scope.All})
	writeJSON(w, map[string]any{"lead": qualificationLead(profile.Lead), "qualification": profile.Qualification, "outcome": profile.Outcome, "outcome_current": profile.Outcome != nil && profile.Outcome.Stage == profile.Lead.Stage && referrals.ClosedStage(profile.Lead.Stage), "close_reasons": referrals.OutcomeReasons()})
}
func normalizeQualification(in *referrals.QualificationInput) {
	in.UseCase = strings.TrimSpace(in.UseCase)
	in.Workload = strings.TrimSpace(in.Workload)
	in.DecisionMaker = strings.TrimSpace(in.DecisionMaker)
	in.Blocker = strings.TrimSpace(in.Blocker)
	if in.BuyingTimeline == "" {
		in.BuyingTimeline = "unknown"
	}
	if in.Priority == "" {
		in.Priority = "unknown"
	}
	if in.ProductFit == "" {
		in.ProductFit = "unknown"
	}
}
func validQualification(in referrals.QualificationInput) bool {
	if in.LeadVersion <= 0 || in.Version < 0 || !boundedText(in.UseCase, api.SalesQualificationTextMaxBytes, true) || !boundedText(in.Workload, api.SalesQualificationTextMaxBytes, true) || !boundedText(in.Blocker, api.SalesQualificationTextMaxBytes, true) || !boundedText(in.DecisionMaker, api.SalesFieldMaxBytes, true) {
		return false
	}
	if !slices.Contains([]string{"unknown", "exploring", "30_days", "90_days", "later", "date"}, in.BuyingTimeline) || !slices.Contains([]string{"unknown", "low", "medium", "high"}, in.Priority) || !slices.Contains([]string{"unknown", "strong", "partial", "poor"}, in.ProductFit) {
		return false
	}
	if in.BuyingTimeline != "date" {
		return in.DecisionOn == ""
	}
	day, err := time.Parse(time.DateOnly, in.DecisionOn)
	return err == nil && day.Year() >= api.SalesTaskMinYear && day.Year() <= api.SalesTaskMaxYear
}
func (s *Service) SaveQualification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.qualificationStore(w, r, acct)
	if !ok {
		return
	}
	lead := r.PathValue("lead")
	var in referrals.QualificationInput
	if !validID(lead) || !decode(w, r, &in) {
		invalid(w, "Choose a lead and provide qualification details.")
		return
	}
	normalizeQualification(&in)
	if !validQualification(in) {
		invalid(w, "Use current lead and qualification versions, valid timeline/priority/fit values, a paired decision date when selected, and text within the displayed limits.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	if err := store.SaveQualification(ctx, scope, lead, in); err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.qualification_saved", acct.ID, map[string]any{"lead_id": lead})
	writeJSON(w, map[string]bool{"saved": true})
}
func (s *Service) QualificationOverview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.qualificationStore(w, r, acct)
	if !ok {
		return
	}
	if !scope.All {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Manager access required", "Only sales managers can view team qualification and outcomes."))
		return
	}
	f := referrals.QualificationFilter{Limit: api.SalesPageSize + 1}
	f.Owner, ok = ownerFilter(w, r, scope)
	if !ok {
		return
	}
	audience := pipelineAudience("qualification", scope, f.Owner)
	var err error
	f.After, err = s.decodeCursor(r.URL.Query().Get("after"), audience)
	if err != nil {
		invalid(w, "invalid qualification cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, counts, reasons, err := store.QualificationOverview(ctx, scope, f)
	if err != nil {
		pipelineError(w, err)
		return
	}
	next := ""
	if len(rows) > api.SalesPageSize {
		rows = rows[:api.SalesPageSize]
		next, err = s.encodeCursor(rows[len(rows)-1].ID, audience)
	}
	if err != nil {
		unavailable(w)
		return
	}
	s.emit(ctx, "sales.qualification_overview", acct.ID, map[string]any{"owner_filter": f.Owner})
	writeJSON(w, map[string]any{"leads": rows, "summary": counts, "outcomes": reasons, "next_cursor": next, "close_reasons": referrals.OutcomeReasons(), "observed_at": time.Now().UTC()})
}
