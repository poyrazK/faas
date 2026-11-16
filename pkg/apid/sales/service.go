package sales

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

type Service struct {
	Config  Config
	Store   referrals.Store
	Key     func() []byte
	Account func(context.Context, string) (state.Account, error)
	Audit   func(context.Context, string, *string, map[string]any)
}

func (s *Service) Enabled() bool { return s.Config.Enabled && s.Store != nil }

func (s *Service) scope(w http.ResponseWriter, r *http.Request, acct state.Account, staff bool) (referrals.Scope, bool) {
	if !s.Enabled() {
		http.NotFound(w, r)
		return referrals.Scope{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !acct.EmailVerified() {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Verify your email", "verify your email before using referrals"))
		return referrals.Scope{}, false
	}
	manager, member := s.Config.Member(acct.Email)
	if staff && (!member || !acct.MFAEnrolled()) {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Sales access required", "a sales team account with an enrolled authenticator is required"))
		return referrals.Scope{}, false
	}
	scope := referrals.Scope{OwnerID: acct.ID, All: staff && manager}
	if !staff {
		scope.Kind = "customer"
	}
	return scope, true
}

func (s *Service) SalesData(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.data(w, r, acct, true)
}
func (s *Service) CustomerData(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.data(w, r, acct, false)
}

func (s *Service) data(w http.ResponseWriter, r *http.Request, acct state.Account, staff bool) {
	scope, ok := s.scope(w, r, acct, staff)
	if !ok {
		return
	}
	audience := acct.ID + ":" + strconv.FormatBool(staff) + ":" + strconv.FormatBool(scope.All)
	after, err := s.decodeCursor(r.URL.Query().Get("after"), audience)
	if err != nil {
		invalid(w, "invalid page cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, err := s.Store.List(ctx, scope, after, api.ReferralPageSize+1)
	if err != nil {
		unavailable(w)
		return
	}
	next := ""
	if len(rows) > api.ReferralPageSize {
		rows = rows[:api.ReferralPageSize]
		next, err = s.encodeCursor(rows[len(rows)-1].AccountID, audience)
		if err != nil {
			unavailable(w)
			return
		}
	}
	summary, err := s.Store.Summary(ctx, scope)
	if err != nil {
		unavailable(w)
		return
	}
	kind := "customer"
	if staff {
		kind = "sales"
	}
	link, err := s.Store.OwnLink(ctx, acct.ID, kind)
	if err != nil && !errors.Is(err, referrals.ErrNotFound) {
		unavailable(w)
		return
	}
	policy := s.Config.Policy
	if !staff {
		policy.CommissionBPS = 0
		redact(rows)
		for i := range summary {
			summary[i].PaidCents = 0
		}
	}

	s.emit(ctx, "sales.referrals_read", acct.ID, map[string]any{"staff": staff, "team_scope": scope.All})
	writeJSON(w, map[string]any{"staff": staff, "manager": scope.All, "email": acct.Email, "policy": policy, "payouts_enabled": false, "referral_code": link, "referrals": rows, "summary": summary, "next_cursor": next, "observed_at": time.Now().UTC()})
}

func redact(rows []referrals.Record) {
	for i := range rows {
		r := &rows[i]
		r.AccountID = ""
		r.OwnerID = ""
		r.Email = ""
		r.Plan = ""
		r.AccountStatus = ""
		r.Stage = ""
		r.Note = ""
		r.FollowUp = ""
		r.Apps = 0
		r.PaidCents = 0
	}
}

func (s *Service) SalesLink(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.createLink(w, r, acct, true)
}
func (s *Service) CustomerLink(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.createLink(w, r, acct, false)
}

func (s *Service) createLink(w http.ResponseWriter, r *http.Request, acct state.Account, staff bool) {
	if _, ok := s.scope(w, r, acct, staff); !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	kind := "customer"
	if staff {
		kind = "sales"
	}
	link, err := s.Store.EnsureLink(ctx, acct.ID, kind)
	if err != nil {
		unavailable(w)
		return
	}
	s.emit(ctx, "sales.referral_link_created", acct.ID, map[string]any{"kind": kind})
	writeJSON(w, map[string]any{"referral_code": link})
}

func (s *Service) UpdateLead(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return
	}
	account := r.PathValue("account")
	if _, err := uuid.Parse(account); err != nil {
		invalid(w, "invalid customer")
		return
	}
	var update referrals.LeadUpdate
	if !decode(w, r, &update) || !validLead(update) {
		invalid(w, "use a valid stage, a note up to 2000 bytes, and a YYYY-MM-DD follow-up date")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	changed, err := s.Store.UpdateLead(ctx, scope, acct.ID, account, update)
	if err != nil {
		unavailable(w)
		return
	}
	if !changed {
		http.NotFound(w, r)
		return
	}
	s.emit(ctx, "sales.lead_updated", acct.ID, map[string]any{"customer_id": account, "stage": update.Stage})
	writeJSON(w, map[string]bool{"updated": true})
}

func validLead(v referrals.LeadUpdate) bool {
	if !utf8.ValidString(v.Note) || len(v.Note) > api.ReferralNoteMaxBytes {
		return false
	}
	if v.Stage != "new" && v.Stage != "contacted" && v.Stage != "qualified" && v.Stage != "lost" {
		return false
	}
	if v.FollowUp != "" {
		_, err := time.Parse(time.DateOnly, v.FollowUp)
		return err == nil
	}
	return true
}

func (s *Service) SetLinkActive(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, ok := s.scope(w, r, acct, strings.HasPrefix(r.URL.Path, "/v1/sales/"))
	if !ok {
		return
	}
	var input struct {
		Active *bool `json:"active"`
	}
	if !decode(w, r, &input) || input.Active == nil || !referrals.ValidCode(r.PathValue("code")) {
		invalid(w, "an active boolean and valid link code are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	changed, err := s.Store.SetLinkActive(ctx, scope, r.PathValue("code"), *input.Active)
	if err != nil {
		unavailable(w)
		return
	}
	if !changed {
		http.NotFound(w, r)
		return
	}
	s.emit(ctx, "sales.referral_link_status", acct.ID, map[string]any{"active": *input.Active})
	writeJSON(w, map[string]bool{"updated": true})
}

func (s *Service) emit(ctx context.Context, event, actor string, data map[string]any) {
	if s.Audit != nil {
		s.Audit(ctx, event, &actor, data)
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, api.ReferralBodyMaxBytes)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return false
	}
	var extra any
	return errors.Is(d.Decode(&extra), io.EOF)
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func invalid(w http.ResponseWriter, detail string) {
	api.WriteProblem(w, api.NewProblem(400, api.CodeBadRequest, "Invalid request", detail))
}
func unavailable(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(503, api.CodeCapacity, "Referral data unavailable", "try again shortly"))
}
