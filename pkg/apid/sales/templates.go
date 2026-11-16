package sales

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) templateStore(w http.ResponseWriter, r *http.Request, acct state.Account) (referrals.TemplateStore, referrals.Scope, bool) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return nil, scope, false
	}
	store, ok := s.Store.(referrals.TemplateStore)
	if !ok {
		unavailable(w)
	}
	return store, scope, ok
}
func templateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, referrals.ErrInvalidTemplate):
		invalid(w, "Use the supported placeholders and text limits. Choose a category, stage and template status.")
	case errors.Is(err, referrals.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Message unavailable", "Choose an active template and a lead assigned to you or your team."))
	case errors.Is(err, referrals.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, "sales_record_conflict", "Template changed", "Refresh and review the latest template before trying again. Your changes were not applied."))
	default:
		unavailable(w)
	}
}
func (s *Service) Templates(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.templateStore(w, r, acct)
	if !ok {
		return
	}
	audience := pipelineAudience("templates", scope)
	after, err := s.decodeCursor(r.URL.Query().Get("after"), audience)
	if err != nil {
		invalid(w, "Invalid template cursor.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, err := store.MessageTemplates(ctx, scope, referrals.TemplateFilter{After: after, Limit: api.SalesPageSize + 1})
	if err != nil {
		templateError(w, err)
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
	writeJSON(w, map[string]any{"templates": rows, "next_cursor": next, "fields": referrals.TemplateFields(), "manager": scope.All})
}
func (s *Service) Template(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.templateStore(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("template")
	if !validID(id) {
		invalid(w, "Choose a valid template.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	row, err := store.MessageTemplate(ctx, scope, id)
	if err != nil {
		templateError(w, err)
		return
	}
	writeJSON(w, row)
}
func (s *Service) SaveTemplate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.templateStore(w, r, acct)
	if !ok {
		return
	}
	if !scope.All {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Manager access required", "Only sales managers can edit message templates."))
		return
	}
	id := r.PathValue("template")
	var in referrals.MessageTemplateInput
	if !decode(w, r, &in) {
		invalid(w, "Provide a message template.")
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Subject = strings.TrimSpace(in.Subject)
	in.Body = strings.TrimSpace(in.Body)
	if !validVersion(id, in.Version) || referrals.ValidateMessageTemplate(in) != nil {
		templateError(w, referrals.ErrInvalidTemplate)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	saved, err := store.SaveMessageTemplate(ctx, scope, id, in)
	if err != nil {
		templateError(w, err)
		return
	}
	s.emit(ctx, "sales.template_saved", acct.ID, map[string]any{"template_id": saved, "status": in.Status})
	writeJSON(w, map[string]string{"id": saved})
}
func (s *Service) PreviewTemplate(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.templateStore(w, r, acct)
	if !ok {
		return
	}
	lead, id := r.PathValue("lead"), r.PathValue("template")
	version, err := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
	if !validID(lead) || !validID(id) || err != nil || version <= 0 {
		invalid(w, "Choose a lead and current template version.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	source, err := store.MessageTemplateSource(ctx, scope, lead, id, version)
	if err != nil {
		templateError(w, err)
		return
	}
	if _, member := s.Config.Member(source.Lead.OwnerEmail); !member {
		source.Lead.ReferralCode = ""
	}
	preview, err := referrals.PersonalizeMessage(source.Template.MessageTemplateInput, source.Lead)
	if err != nil {
		templateError(w, err)
		return
	}
	s.emit(ctx, "sales.template_preview", acct.ID, map[string]any{"lead_id": lead, "template_id": id, "template_version": version})
	writeJSON(w, map[string]any{"template": source.Template, "lead": source.Lead, "message": preview, "observed_at": time.Now().UTC()})
}
