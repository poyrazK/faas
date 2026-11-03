package sales

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) pipeline(w http.ResponseWriter, r *http.Request, acct state.Account) (referrals.PipelineStore, referrals.Scope, bool) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return nil, scope, false
	}
	store, ok := s.Store.(referrals.PipelineStore)
	if !ok {
		pipelineError(w, errors.New("pipeline store unavailable"))
	}
	return store, scope, ok
}
func (s *Service) team(ctx context.Context, store referrals.PipelineStore) ([]referrals.Rep, error) {
	emails := make([]string, 0, len(s.Config.Members))
	for email := range s.Config.Members {
		emails = append(emails, email)
	}
	return store.SalesTeam(ctx, emails)
}
func (s *Service) Team(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.pipeline(w, r, acct)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	team, err := s.team(ctx, store)
	if err != nil {
		pipelineError(w, err)
		return
	}
	writeJSON(w, map[string]any{"actor_id": acct.ID, "manager": scope.All, "members": team, "stages": referrals.ProspectStages(), "close_reasons": referrals.OutcomeReasons(), "refresh_seconds": api.SalesTaskRefreshSeconds})
}
func validID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && id == v.String()
}
func validStage(stage string) bool { return slices.Contains(referrals.ProspectStages(), stage) }
func ownerFilter(w http.ResponseWriter, r *http.Request, scope referrals.Scope) (string, bool) {
	owner := r.URL.Query().Get("owner")
	if owner != "" && (!validID(owner) || (!scope.All && owner != scope.OwnerID)) {
		invalid(w, "use your own owner ID, or a team owner when signed in as a manager")
		return "", false
	}
	return owner, true
}

func (s *Service) Leads(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.pipeline(w, r, acct)
	if !ok {
		return
	}
	f := referrals.PipelineFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Stage: r.URL.Query().Get("stage"), Limit: api.SalesPageSize + 1}
	f.Owner, ok = ownerFilter(w, r, scope)
	if !ok {
		return
	}
	if !utf8.ValidString(f.Query) || len(f.Query) > api.SalesSearchMaxBytes || (f.Stage != "" && !validStage(f.Stage)) {
		invalid(w, "invalid lead filters")
		return
	}
	audience := pipelineAudience("leads", scope, f.Owner, f.Query, f.Stage)
	var err error
	f.After, err = s.decodeCursor(r.URL.Query().Get("after"), audience)
	if err != nil {
		invalid(w, "invalid page cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, counts, err := store.Prospects(ctx, scope, f)
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
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.prospects_read", acct.ID, map[string]any{"team_scope": scope.All})
	writeJSON(w, map[string]any{"leads": rows, "summary": counts, "next_cursor": next})
}

func normalizeProspect(in *referrals.ProspectInput) {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Company = strings.TrimSpace(in.Company)
	in.Note = strings.TrimSpace(in.Note)
	in.CloseReason = strings.TrimSpace(in.CloseReason)
	in.CloseNote = strings.TrimSpace(in.CloseNote)
}
func validProspect(in referrals.ProspectInput) bool {
	parsed, err := mail.ParseAddress(in.Email)
	return err == nil && parsed.Address == in.Email && len(in.Email) <= api.SalesEmailMaxBytes && validID(in.OwnerID) && validStage(in.Stage) &&
		boundedText(in.Name, api.SalesNameMaxBytes, false) && boundedText(in.Company, api.SalesFieldMaxBytes, true) && boundedText(in.Note, api.ReferralNoteMaxBytes, true) && validClosingFields(in)
}
func validClosingFields(in referrals.ProspectInput) bool {
	if !boundedText(in.CloseNote, api.SalesOutcomeNoteMaxBytes, true) {
		return false
	}
	if in.CloseReason == "" {
		return in.CloseNote == ""
	}
	return referrals.ValidOutcomeReason(in.Stage, in.CloseReason) && (in.CloseReason != "other" || in.CloseNote != "")
}
func boundedText(value string, max int, empty bool) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && len(value) <= max && (empty || value != "")
}
func validVersion(id string, version int64) bool {
	return (id == "" && version == 0) || (validID(id) && version > 0)
}
func (s *Service) checkOwner(ctx context.Context, w http.ResponseWriter, store referrals.PipelineStore, scope referrals.Scope, owner string) bool {
	if !scope.All && owner != scope.OwnerID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Manager access required", "Only sales managers can assign another salesperson."))
		return false
	}
	team, err := s.team(ctx, store)
	if err != nil {
		pipelineError(w, err)
		return false
	}
	for _, rep := range team {
		if rep.ID == owner {
			return true
		}
	}
	invalid(w, "Choose an active sales team member with verified email and MFA.")
	return false
}

func (s *Service) SaveProspect(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.pipeline(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("lead")
	var in referrals.ProspectInput
	if !decode(w, r, &in) {
		invalid(w, "invalid lead body")
		return
	}
	normalizeProspect(&in)
	if id == "" {
		if in.OwnerID == "" {
			in.OwnerID = acct.ID
		}
		if in.Stage == "" {
			in.Stage = "new"
		}
	}
	if !validVersion(id, in.Version) || !validProspect(in) || (id == "" && referrals.ClosedStage(in.Stage) && in.CloseReason == "") {
		invalid(w, "Provide a name, valid email, owner, stage and current version; notes allow 2000 bytes. New won/lost leads need a valid closing reason; explain Other within 1000 bytes.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	if !s.checkOwner(ctx, w, store, scope, in.OwnerID) {
		return
	}
	saved, err := store.SaveProspect(ctx, scope, acct.ID, id, in)
	if err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.prospect_saved", acct.ID, map[string]any{"lead_id": saved, "owner_id": in.OwnerID, "stage": in.Stage, "created": id == ""})
	writeJSON(w, map[string]string{"id": saved})
}
func (s *Service) DeleteProspect(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.pipeline(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("lead")
	version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
	if err != nil || !validVersion(id, version) {
		invalid(w, "Provide a valid lead and current version.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	changed, err := store.DeleteProspect(ctx, scope, id, version)
	if err != nil {
		pipelineError(w, err)
		return
	}
	if !changed {
		pipelineError(w, referrals.ErrConflict)
		return
	}
	s.emit(ctx, "sales.prospect_deleted", acct.ID, map[string]any{"lead_id": id})
	writeJSON(w, map[string]bool{"deleted": true})
}
func pipelineAudience(kind string, scope referrals.Scope, filters ...string) string {
	// Length-prefix values so query text cannot create an ambiguous scope.
	out := kind + ":" + scope.OwnerID + ":" + strconv.FormatBool(scope.All)
	for _, f := range filters {
		out += ":" + strconv.Itoa(len(f)) + ":" + f
	}
	return out
}
func pipelineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, referrals.ErrOutcomeRequired), errors.Is(err, referrals.ErrInvalidOutcome):
		api.WriteProblem(w, api.NewProblem(400, "sales_outcome_required", "Closing reason required", "Choose a reason for won/lost and explain Other. Refresh the lead if its stage changed."))
	case errors.Is(err, referrals.ErrDuplicateLead):
		api.WriteProblem(w, api.NewProblem(409, "sales_lead_exists", "Lead already exists", "This salesperson already has a lead with that email. Search for the existing lead."))
	case errors.Is(err, referrals.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "sales_record_conflict", "Record changed or unavailable", "Close the editor, refresh the list and review the latest record before saving. Your changes were not applied."))
	case errors.Is(err, referrals.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Lead unavailable", "Choose a lead assigned to you or your team."))
	default:
		api.WriteProblem(w, api.NewProblem(503, api.CodeCapacity, "Sales data unavailable", "Try again shortly."))
	}
}
