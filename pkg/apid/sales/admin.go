package sales

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	salesReasonMaxBytes    = 500
	salesReferenceMaxBytes = 200
	agreementMaxFutureDays = 366
)

// teamStore resolves the staff scope and the team store. admin requires an
// owner admin; otherwise managers pass and salespeople get 403.
func (s *Service) teamStore(w http.ResponseWriter, r *http.Request, acct state.Account, admin bool) (referrals.TeamStore, referrals.Scope, bool) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return nil, scope, false
	}
	if admin && !scope.Admin() {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner admin required", "Only an owner admin can manage agreements, credit and payouts."))
		return nil, scope, false
	}
	if !admin && !scope.All {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Manager access required", "Only sales managers and owner admins can view this."))
		return nil, scope, false
	}
	store, ok := s.Store.(referrals.TeamStore)
	if !ok {
		unavailable(w)
		return nil, scope, false
	}
	return store, scope, true
}

// AdminTeam lists every member with the effective role, its source and whether
// the account can sign in to sales (managers and owner admins). Commission
// terms are owner-admin data, so managers get the roster without them.
func (s *Service) AdminTeam(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.teamStore(w, r, acct, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	team, err := s.roster(ctx)
	if err != nil {
		unavailable(w)
		return
	}
	members, err := store.TeamMembers(ctx, s.configRoster().emails())
	if err != nil {
		unavailable(w)
		return
	}
	for i := range members {
		m := &members[i]
		m.ConfigRole = s.Config.ConfigRole(m.Email)
		m.Role = team.role(m.Email)
		m.Ready = m.Role != "" && m.AccountStatus == string(state.AccountActive) && m.EmailVerified && m.MFAEnrolled
		if !scope.Admin() {
			m.CommissionBPS, m.CommissionAt = nil, nil
		}
	}
	s.emit(ctx, "sales.team_read", acct.ID, nil)
	out := map[string]any{"members": members, "roles": []string{referrals.RoleManager, referrals.RoleRep},
		"can_manage_terms": scope.Admin(), "observed_at": time.Now().UTC()}
	if scope.Admin() {
		out["default_commission_bps"] = s.Config.Policy.CommissionBPS
		out["currency"] = s.Config.Policy.Currency
	}
	writeJSON(w, out)
}

type memberInput struct {
	Email   string `json:"email"`
	Role    string `json:"role"`
	Active  *bool  `json:"active"`
	Version int64  `json:"version"`
}

func validStoredRole(role string) bool {
	return role == referrals.RoleManager || role == referrals.RoleRep
}

// managersOnlyReps refuses a manager acting on anything but a salesperson.
// Adding, changing or removing managers stays with owner admins.
func managersOnlyReps(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner admin required", "Sales managers can add, deactivate and reactivate salespeople. Ask an owner admin to add, change or remove a manager."))
}

// repRecord reports whether account's stored row is a salesperson that holds
// no stronger role anywhere. The update that follows only applies at the
// caller's version, and any change to the row bumps it, so a row changed
// after this read fails as a conflict instead of being overwritten.
func (s *Service) repRecord(ctx context.Context, store referrals.TeamStore, account string) (bool, error) {
	team, err := s.roster(ctx)
	if err != nil {
		return false, err
	}
	members, err := store.TeamMembers(ctx, s.configRoster().emails())
	if err != nil {
		return false, err
	}
	for _, m := range members {
		if m.AccountID == account {
			effective := team.role(m.Email)
			return m.StoredRole == referrals.RoleRep && (effective == "" || effective == referrals.RoleRep), nil
		}
	}
	return false, nil
}

// AddMember grants an existing Gregale account the manager or rep role.
// Owner admins add either; managers add salespeople only.
func (s *Service) AddMember(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.teamStore(w, r, acct, false)
	if !ok {
		return
	}
	var in memberInput
	if !decode(w, r, &in) || in.Active != nil || in.Version != 0 || !validStoredRole(in.Role) {
		invalid(w, "Send an email and a role of manager or rep.")
		return
	}
	if !scope.Admin() && in.Role != referrals.RoleRep {
		managersOnlyReps(w)
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if parsed, err := mail.ParseAddress(email); err != nil || parsed.Address != email || len(email) > api.SalesEmailMaxBytes {
		invalid(w, "Enter a valid email address.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	account, err := store.AccountIDByEmail(ctx, email)
	if errors.Is(err, referrals.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(404, "sales_account_not_found", "No Gregale account", "Ask the person to create a Gregale account with this email, verify it and enroll an authenticator, then add them."))
		return
	}
	if err != nil {
		unavailable(w)
		return
	}
	if err = store.SaveTeamMember(ctx, acct.ID, account, in.Role, true, 0); err != nil {
		if errors.Is(err, referrals.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(409, "sales_member_exists", "Already on the team", "This account already has a team record. Refresh and change its role or reactivate it instead."))
			return
		}
		unavailable(w)
		return
	}
	s.emit(ctx, "sales.team_member_added", acct.ID, map[string]any{"member_id": account, "role": in.Role})
	writeJSON(w, map[string]any{"account_id": account, "role": in.Role, "active": true})
}

// UpdateMember changes a portal member's role or deactivates/reactivates it.
// Config-granted roles are unaffected; the strongest grant still applies.
// Owner admins change anyone; managers deactivate or reactivate salespeople
// and cannot promote them.
func (s *Service) UpdateMember(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, scope, ok := s.teamStore(w, r, acct, false)
	if !ok {
		return
	}
	account := r.PathValue("member")
	var in memberInput
	if !validID(account) || !decode(w, r, &in) || in.Email != "" || in.Active == nil || in.Version <= 0 || !validStoredRole(in.Role) {
		invalid(w, "Send role (manager or rep), active and the current version.")
		return
	}
	if account == acct.ID {
		invalid(w, "You cannot change your own role or deactivate yourself. Ask another manager or an owner admin.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	if !scope.Admin() {
		if in.Role != referrals.RoleRep {
			managersOnlyReps(w)
			return
		}
		rep, err := s.repRecord(ctx, store, account)
		if err != nil {
			unavailable(w)
			return
		}
		if !rep {
			managersOnlyReps(w)
			return
		}
	}
	if err := store.SaveTeamMember(ctx, acct.ID, account, in.Role, *in.Active, in.Version); err != nil {
		pipelineError(w, err)
		return
	}
	s.emit(ctx, "sales.team_member_updated", acct.ID, map[string]any{"member_id": account, "role": in.Role, "active": *in.Active})
	writeJSON(w, map[string]any{"updated": true})
}

type agreementInput struct {
	CommissionBPS *int32 `json:"commission_bps"`
	EffectiveFrom string `json:"effective_from"`
	Note          string `json:"note"`
}

// Agreements returns one salesperson's agreement history (owner admin).
func (s *Service) Agreements(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, _, ok := s.teamStore(w, r, acct, true)
	if !ok {
		return
	}
	account := r.PathValue("member")
	if !validID(account) {
		invalid(w, "Choose a team member.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, err := store.Agreements(ctx, account)
	if err != nil {
		unavailable(w)
		return
	}
	writeJSON(w, map[string]any{"agreements": rows, "default_commission_bps": s.Config.Policy.CommissionBPS})
}

// AddAgreement records a salesperson's new commission rate. It applies to
// customers attributed from effective_from on; existing customers keep the
// rate captured when they were attributed.
func (s *Service) AddAgreement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, _, ok := s.teamStore(w, r, acct, true)
	if !ok {
		return
	}
	account := r.PathValue("member")
	var in agreementInput
	if !validID(account) || !decode(w, r, &in) || in.CommissionBPS == nil || *in.CommissionBPS < 0 || *in.CommissionBPS > api.ReferralCommissionMaxBPS || !boundedText(in.Note, salesReasonMaxBytes, true) {
		invalid(w, "Send commission_bps from 0 to 10000 (100 = 1%), an optional YYYY-MM-DD effective_from and a note up to 500 bytes.")
		return
	}
	now := time.Now().UTC()
	from := now
	if in.EffectiveFrom != "" {
		day, err := time.Parse(time.DateOnly, in.EffectiveFrom)
		today := now.Truncate(24 * time.Hour)
		if err != nil || day.Before(today) || day.After(today.AddDate(0, 0, agreementMaxFutureDays)) {
			invalid(w, "effective_from must be today or a later date within a year; agreements never change past customers.")
			return
		}
		if day.After(today) {
			from = day
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	team, err := s.roster(ctx)
	if err != nil {
		unavailable(w)
		return
	}
	member, err := s.memberAccount(ctx, team, account)
	if err != nil {
		memberError(w, err)
		return
	}
	agreement, err := store.AddAgreement(ctx, acct.ID, member.ID, referrals.AgreementInput{CommissionBPS: *in.CommissionBPS, EffectiveFrom: from, Note: in.Note})
	if err != nil {
		unavailable(w)
		return
	}
	s.emit(ctx, "sales.agreement_added", acct.ID, map[string]any{"member_id": member.ID, "commission_bps": *in.CommissionBPS, "effective_from": from})
	writeJSON(w, map[string]any{"agreement": agreement})
}

var errNotMember = errors.New("not a sales team member")

// memberAccount loads a current team member's account by ID.
func (s *Service) memberAccount(ctx context.Context, team roster, id string) (state.Account, error) {
	if s.Account == nil {
		return state.Account{}, errNotMember
	}
	member, err := s.Account(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		return state.Account{}, errNotMember
	}
	if err != nil {
		return state.Account{}, err
	}
	if team.role(member.Email) == "" {
		return state.Account{}, errNotMember
	}
	return member, nil
}

func memberError(w http.ResponseWriter, err error) {
	if errors.Is(err, errNotMember) {
		api.WriteProblem(w, api.NewProblem(404, "sales_member_unavailable", "Not on the sales team", "Choose a current sales team member."))
		return
	}
	unavailable(w)
}

// CreditHistory lists moves of one customer's sales credit (managers, admins).
func (s *Service) CreditHistory(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, _, ok := s.teamStore(w, r, acct, false)
	if !ok {
		return
	}
	customer := r.PathValue("account")
	if !validID(customer) {
		invalid(w, "Choose a customer.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, err := store.CreditHistory(ctx, customer)
	if err != nil {
		unavailable(w)
		return
	}
	writeJSON(w, map[string]any{"changes": rows})
}

// MoveCredit moves one customer's sales credit to another salesperson, who
// must be able to sign in to sales. The new owner's current agreement applies.
func (s *Service) MoveCredit(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, _, ok := s.teamStore(w, r, acct, true)
	if !ok {
		return
	}
	customer := r.PathValue("account")
	var in struct {
		OwnerID string `json:"owner_id"`
		Reason  string `json:"reason"`
	}
	if !validID(customer) || !decode(w, r, &in) || !validID(in.OwnerID) || strings.TrimSpace(in.Reason) == "" || !boundedText(in.Reason, salesReasonMaxBytes, false) {
		invalid(w, "Send the new owner_id and a reason up to 500 bytes.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	team, err := s.roster(ctx)
	if err != nil {
		unavailable(w)
		return
	}
	target, err := s.memberAccount(ctx, team, in.OwnerID)
	if err == nil && (target.Status != state.AccountActive || !target.EmailVerified() || !target.MFAEnrolled()) {
		err = errNotMember
	}
	if err != nil {
		memberError(w, err)
		return
	}
	change, err := store.MoveCredit(ctx, acct.ID, customer, target.ID, in.Reason, s.Config.Policy.CommissionBPS)
	if err != nil {
		creditError(w, err)
		return
	}
	s.emit(ctx, "sales.credit_moved", acct.ID, map[string]any{"customer_id": customer, "from_owner_id": change.FromOwnerID, "to_owner_id": change.ToOwnerID,
		"from_commission_bps": change.FromCommissionBPS, "to_commission_bps": change.ToCommissionBPS})
	writeJSON(w, map[string]any{"change": change})
}

func creditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, referrals.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Customer unavailable", "Choose a customer with a referral."))
	case errors.Is(err, referrals.ErrNotSalesCredit):
		api.WriteProblem(w, api.NewProblem(409, "sales_credit_customer_referral", "Customer referral", "This customer came from a customer referral code, not a salesperson. It cannot be moved to a salesperson."))
	case errors.Is(err, referrals.ErrPayoutRecorded):
		api.WriteProblem(w, api.NewProblem(409, "sales_payout_recorded", "Payout already recorded", "A payout is recorded for this customer. Void it first if it was recorded by mistake."))
	case errors.Is(err, referrals.ErrSameOwner):
		api.WriteProblem(w, api.NewProblem(409, "sales_credit_same_owner", "Already credited", "This customer is already credited to that salesperson."))
	case errors.Is(err, referrals.ErrPayoutNotEligible):
		api.WriteProblem(w, api.NewProblem(409, "sales_payout_not_eligible", "Not eligible yet", "Record a payout only after the commission estimate is eligible (paid, not refunded, hold period over)."))
	default:
		unavailable(w)
	}
}

// Payouts lists recorded payouts: salespeople see their own, managers and
// owner admins see everyone's.
func (s *Service) Payouts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, ok := s.scope(w, r, acct, true)
	if !ok {
		return
	}
	store, ok := s.Store.(referrals.TeamStore)
	if !ok {
		unavailable(w)
		return
	}
	owner, ok := ownerFilter(w, r, scope)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	rows, err := store.Payouts(ctx, scope, owner, r.URL.Query().Get("voided") == "1")
	if err != nil {
		unavailable(w)
		return
	}
	s.emit(ctx, "sales.payouts_read", acct.ID, map[string]any{"team_scope": scope.All})
	writeJSON(w, map[string]any{"payouts": rows, "observed_at": time.Now().UTC()})
}

// RecordPayout books a commission payment made outside Gregale.
func (s *Service) RecordPayout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, _, ok := s.teamStore(w, r, acct, true)
	if !ok {
		return
	}
	customer := r.PathValue("account")
	var in referrals.PayoutInput
	if !validID(customer) || !decode(w, r, &in) || in.AmountCents < 1 || in.AmountCents > api.ReferralRewardMaxCents ||
		!boundedText(in.Reference, salesReferenceMaxBytes, true) || !boundedText(in.Note, salesReasonMaxBytes, true) {
		invalid(w, "Send amount_cents (positive whole cents), paid_on (YYYY-MM-DD), an optional reference up to 200 bytes and note up to 500 bytes.")
		return
	}
	day, err := time.Parse(time.DateOnly, in.PaidOn)
	if err != nil || day.After(time.Now().UTC().AddDate(0, 0, 1)) || day.Year() < 2000 {
		invalid(w, "paid_on must be a real date, not in the future.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	payout, err := store.RecordPayout(ctx, acct.ID, customer, in)
	if err != nil {
		creditError(w, err)
		return
	}
	s.emit(ctx, "sales.payout_recorded", acct.ID, map[string]any{"customer_id": customer, "owner_id": payout.OwnerID, "amount_cents": payout.AmountCents, "currency": payout.Currency})
	writeJSON(w, map[string]any{"payout": payout})
}

// VoidPayout cancels a mistaken payout record; the customer can be paid again.
func (s *Service) VoidPayout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, _, ok := s.teamStore(w, r, acct, true)
	if !ok {
		return
	}
	id := r.PathValue("payout")
	var in struct {
		Reason string `json:"reason"`
	}
	if !validID(id) || !decode(w, r, &in) || strings.TrimSpace(in.Reason) == "" || !boundedText(in.Reason, salesReasonMaxBytes, false) {
		invalid(w, "Send a reason up to 500 bytes.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.ReferralReadTimeout)
	defer cancel()
	changed, err := store.VoidPayout(ctx, acct.ID, id, in.Reason)
	if err != nil {
		unavailable(w)
		return
	}
	if !changed {
		api.WriteProblem(w, api.NewProblem(404, "not_found", "Payout unavailable", "The payout does not exist or is already void."))
		return
	}
	s.emit(ctx, "sales.payout_voided", acct.ID, map[string]any{"payout_id": id})
	writeJSON(w, map[string]any{"voided": true})
}
