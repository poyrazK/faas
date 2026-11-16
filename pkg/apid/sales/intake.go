package sales

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) intakeEnabled(w http.ResponseWriter, r *http.Request) bool {
	if !s.Enabled() || !s.Config.IntakeEnabled {
		http.NotFound(w, r)
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	return true
}
func (s *Service) intakeKey() []byte {
	if s.Key == nil {
		return nil
	}
	return s.Key()
}
func intakeSignature(key []byte, payload string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("gregale-sales-intake-v1:" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func makeIntakeToken(key []byte, now time.Time) string {
	payload := uuid.NewString() + "." + strconv.FormatInt(now.UnixMilli(), 10)
	return payload + "." + intakeSignature(key, payload)
}
func parseIntakeToken(key []byte, token string, now time.Time) (string, time.Time, error) {
	parts := strings.Split(token, ".")
	if len(key) < sha256.Size || len(parts) != 3 || !validID(parts[0]) {
		return "", time.Time{}, fmt.Errorf("invalid challenge")
	}
	payload := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(intakeSignature(key, payload))) {
		return "", time.Time{}, fmt.Errorf("invalid challenge")
	}
	ms, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", time.Time{}, err
	}
	issued := time.UnixMilli(ms)
	age := now.Sub(issued)
	if age < api.SalesIntakeChallengeMinAge || age > api.SalesIntakeChallengeTTL {
		return "", time.Time{}, fmt.Errorf("expired or premature challenge")
	}
	return parts[0], issued.Add(api.SalesIntakeChallengeTTL), nil
}
func (s *Service) IntakeChallenge(w http.ResponseWriter, r *http.Request) {
	if !s.intakeEnabled(w, r) {
		return
	}
	key := s.intakeKey()
	if len(key) < sha256.Size {
		unavailable(w)
		return
	}
	writeJSON(w, map[string]any{"challenge": makeIntakeToken(key, time.Now()), "ready_after_ms": api.SalesIntakeChallengeMinAge.Milliseconds(), "expires_in_seconds": int(api.SalesIntakeChallengeTTL.Seconds())})
}
func normalizeInquiry(in *referrals.InquiryInput) {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Company = strings.TrimSpace(in.Company)
	in.Message = strings.TrimSpace(in.Message)
	in.Code = referrals.NormalizeCode(in.Code)
}
func validInquiry(in referrals.InquiryInput) bool {
	email, err := mail.ParseAddress(in.Email)
	return err == nil && email.Address == in.Email && len(in.Email) <= api.SalesEmailMaxBytes &&
		boundedText(in.Name, api.SalesNameMaxBytes, false) && boundedText(in.Company, api.SalesFieldMaxBytes, true) && boundedText(in.Message, api.SalesIntakeMessageMaxBytes, false) &&
		(in.Intent == "contact" || in.Intent == "demo") && (in.Code == "" || referrals.ValidCode(in.Code))
}
func (s *Service) intakeOptions() referrals.IntakeOptions {
	o := referrals.IntakeOptions{MemberEmails: []string{}, RepEmails: []string{}}
	for email, manager := range s.Config.Members {
		o.MemberEmails = append(o.MemberEmails, email)
		if !manager {
			o.RepEmails = append(o.RepEmails, email)
		}
	}
	zone := s.Config.IntakeTimezone
	if zone == nil {
		zone = time.UTC
	}
	o.DueOn = time.Now().In(zone).AddDate(0, 0, s.Config.IntakeFollowupDays).Format(time.DateOnly)
	return o
}
func (s *Service) SubmitInquiry(w http.ResponseWriter, r *http.Request) {
	if !s.intakeEnabled(w, r) {
		return
	}
	var body struct {
		referrals.InquiryInput
		Challenge string `json:"challenge"`
		Website   string `json:"website"`
	}
	if !decode(w, r, &body) {
		invalid(w, "Provide your name, email, inquiry type and message.")
		return
	}
	if body.Website != "" {
		acceptedInquiry(w)
		return
	}
	normalizeInquiry(&body.InquiryInput)
	if !validInquiry(body.InquiryInput) {
		invalid(w, "Provide a name, valid email, contact or demo request, message up to 1500 bytes, and an optional six-character salesperson code.")
		return
	}
	nonce, expires, err := parseIntakeToken(s.intakeKey(), body.Challenge, time.Now())
	if err != nil {
		api.WriteProblem(w, api.NewProblem(400, "sales_challenge_invalid", "Please try again", "Refresh the form check and submit again."))
		return
	}
	store, ok := s.Store.(referrals.IntakeStore)
	if !ok {
		unavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	if err = store.SubmitInquiry(ctx, nonce, expires, body.InquiryInput, s.intakeOptions()); err != nil {
		unavailable(w)
		return
	}
	// An unverified submission is never attributed to a staff account in the audit trail.
	acceptedInquiry(w)
}
func acceptedInquiry(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("{\"accepted\":true}\n"))
}

func (s *Service) inquiryStore(w http.ResponseWriter, r *http.Request, acct state.Account) (referrals.IntakeStore, referrals.Scope, bool) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return nil, scope, false
	}
	store, ok := s.Store.(referrals.IntakeStore)
	if !ok {
		unavailable(w)
	}
	return store, scope, ok
}
func (s *Service) Inquiries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.inquiryStore(w, r, acct)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "pending" && status != "assigned" && status != "closed" {
		invalid(w, "Choose pending, assigned, closed or all inquiries.")
		return
	}
	audience := pipelineAudience("inquiries", scope, status)
	after, err := s.decodeCursor(r.URL.Query().Get("after"), audience)
	if err != nil {
		invalid(w, "invalid page cursor")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, err := store.Inquiries(ctx, scope, referrals.InquiryFilter{Status: status, After: after, Limit: api.SalesPageSize + 1})
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
	s.emit(ctx, "sales.inquiries_read", acct.ID, map[string]any{"team_scope": scope.All})
	writeJSON(w, map[string]any{"inquiries": rows, "next_cursor": next})
}
func (s *Service) AssignInquiry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.inquiryStore(w, r, acct)
	if !ok {
		return
	}
	if !scope.All {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Manager access required", "Only managers can route inquiries."))
		return
	}
	var in struct {
		OwnerID string `json:"owner_id"`
		Version int64  `json:"version"`
	}
	id := r.PathValue("inquiry")
	if !decode(w, r, &in) || !validID(in.OwnerID) || !validVersion(id, in.Version) {
		invalid(w, "Choose a salesperson and the current inquiry version.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	if err := store.AssignInquiry(ctx, id, in.Version, in.OwnerID, s.intakeOptions()); err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.inquiry_assigned", acct.ID, map[string]any{"inquiry_id": id, "owner_id": in.OwnerID})
	writeJSON(w, map[string]bool{"assigned": true})
}
func (s *Service) CloseInquiry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.inquiryStore(w, r, acct)
	if !ok {
		return
	}
	var in struct {
		Version int64 `json:"version"`
	}
	id := r.PathValue("inquiry")
	if !decode(w, r, &in) || !validVersion(id, in.Version) {
		invalid(w, "Provide the current inquiry version.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	changed, err := store.CloseInquiry(ctx, scope, id, in.Version)
	if err != nil {
		pipelineError(w, err)
		return
	}
	if !changed {
		pipelineError(w, referrals.ErrConflict)
		return
	}
	s.emit(ctx, "sales.inquiry_closed", acct.ID, map[string]any{"inquiry_id": id})
	writeJSON(w, map[string]bool{"closed": true})
}
