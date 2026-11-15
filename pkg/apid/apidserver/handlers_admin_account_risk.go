package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Account risk view bounds (ADR-20261115090000).
const (
	accountRiskEventWindow  = 30 * 24 * time.Hour
	accountRiskEventLimit   = 100
	heldLinksDefaultWindow  = 24 * time.Hour
	heldLinksMaxWindow      = 30 * 24 * time.Hour
	heldLinksDefaultLimit   = 200
	heldLinksMaxLimit       = 1000
	accountRiskStrikeWindow = time.Duration(api.EgressFanoutHoldWindowSeconds) * time.Second
)

// accountRiskEventKinds are the abuse events the view lists: holds, CPU
// signals, build-time abuse scans and abuse recycles.
var accountRiskEventKinds = []string{
	"accounts.abuse_hold", "instance.cpu_abuse", "deployment.abuse_scan",
	"egress_fanout", "egress_flood", state.AccountAbuseHoldCPUSustained,
}

// getAccountRisk is GET /v1/admin/accounts/{id}/risk: the account's hold,
// sign-up record, links to and from abuse-held accounts, strikes in the
// hold window and abuse events of the last 30 days. Email, sign-up address
// and user agent need ?include_pii=1 (audited as pii.accessed).
func (s *server) getAccountRisk(w http.ResponseWriter, r *http.Request, caller state.Account) {
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad account id", "id must be a UUID"))
		return
	}
	risk, ok := s.store.(state.AccountRiskStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("the account risk view is not available on this store"))
		return
	}
	acct, err := s.store.AccountByID(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not Found", "no account with that id"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the account"))
		return
	}
	includePII, _ := strconv.ParseBool(r.URL.Query().Get("include_pii"))
	if includePII {
		emitPIIAccessed(r, s, caller, "GET /v1/admin/accounts/{id}/risk")
	}
	resp, err := s.buildAccountRisk(r.Context(), risk, acct, includePII, timeNow().UTC())
	if err != nil {
		s.log.Warn("apid: account risk view", "account", id, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not build the account risk view"))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) buildAccountRisk(ctx context.Context, risk state.AccountRiskStore, acct state.Account, includePII bool, now time.Time) (api.AccountRiskResponse, error) {
	resp := api.AccountRiskResponse{
		AccountID: acct.ID, Status: string(acct.Status), Plan: string(acct.Plan), CreatedAt: acct.CreatedAt.UTC(),
		AbuseHoldAt: acct.AbuseHoldAt, AbuseHoldReason: acct.AbuseHoldReason,
		LinksToHeld: []api.AccountRiskLink{}, LinkedAccounts: []api.AccountRiskLink{},
		Strikes: []api.AccountRiskStrike{}, AbuseEvents: []api.AccountRiskEvent{},
	}
	if includePII {
		resp.Email = acct.Email
	}
	if facts, ok := s.store.(state.AccountSignupFactsStore); ok {
		f, err := facts.AccountSignupFacts(ctx, acct.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return resp, err
		}
		if err == nil {
			resp.Signup = accountRiskSignup(f, includePII)
		}
	}
	if links, ok := s.store.(state.AccountHeldLinkStore); ok {
		to, err := links.AccountHeldLinks(ctx, acct.ID)
		if err != nil {
			return resp, err
		}
		resp.LinksToHeld = appendRiskLinks(resp.LinksToHeld, to)
	}
	from, err := risk.HeldLinksToAccount(ctx, acct.ID)
	if err != nil {
		return resp, err
	}
	resp.LinkedAccounts = appendRiskLinks(resp.LinkedAccounts, from)
	strikes, err := risk.AccountAbuseStrikes(ctx, acct.ID, now.Add(-accountRiskStrikeWindow))
	if err != nil {
		return resp, err
	}
	for _, st := range strikes {
		resp.Strikes = append(resp.Strikes, api.AccountRiskStrike{Reason: st.Reason, AppID: st.AppID, StruckAt: st.StruckAt})
	}
	events, err := risk.AccountAbuseEvents(ctx, acct.ID, accountRiskEventKinds, now.Add(-accountRiskEventWindow), accountRiskEventLimit)
	if err != nil {
		return resp, err
	}
	for _, e := range events {
		resp.AbuseEvents = append(resp.AbuseEvents, api.AccountRiskEvent{At: e.At, Kind: e.Kind, Actor: e.Actor, Subject: e.Subject, Data: e.Data})
	}
	return resp, nil
}

func accountRiskSignup(f state.AccountSignupFacts, includePII bool) *api.AccountRiskSignup {
	out := &api.AccountRiskSignup{
		Method: f.Method, Country: f.Country, ASN: f.ASN, ASNOrg: f.ASNOrg,
		EmailDomain: f.EmailDomain, DisposableEmail: f.DisposableEmail, CreatedAt: f.CreatedAt, ReducedAt: f.ReducedAt,
	}
	if includePII {
		out.SignupIP, out.UserAgent = f.SignupIP, f.UserAgent
	}
	return out
}

func riskLink(l state.AccountHeldLink) api.AccountRiskLink {
	return api.AccountRiskLink{
		AccountID: l.AccountID, HeldAccountID: l.HeldAccountID, HeldOrgID: l.HeldOrgID,
		HeldReason: l.HeldReason, Signal: l.Signal, DetectedAt: l.DetectedAt,
	}
}

func appendRiskLinks(dst []api.AccountRiskLink, links []state.AccountHeldLink) []api.AccountRiskLink {
	for _, l := range links {
		dst = append(dst, riskLink(l))
	}
	return dst
}

// listHeldLinks is GET /v1/admin/held-links: links to abuse-held accounts
// detected in the last `since` (a Go duration, default 24h, at most 30 days),
// newest first, for the daily review. Names no address, so no PII opt-in.
func (s *server) listHeldLinks(w http.ResponseWriter, r *http.Request, _ state.Account) {
	window, limit, prob := parseHeldLinksQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	risk, ok := s.store.(state.AccountRiskStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("held-account links are not available on this store"))
		return
	}
	rows, err := risk.RecentHeldLinks(r.Context(), timeNow().UTC().Add(-window), limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list held-account links"))
		return
	}
	resp := api.HeldLinksResponse{Links: make([]api.HeldLinkReview, 0, len(rows)), Truncated: len(rows) >= limit}
	for _, row := range rows {
		resp.Links = append(resp.Links, api.HeldLinkReview{
			AccountRiskLink: riskLink(row.AccountHeldLink), Country: row.Country,
			EmailDomain: row.EmailDomain, DisposableEmail: row.DisposableEmail,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseHeldLinksQuery(r *http.Request) (time.Duration, int, *api.Problem) {
	bad := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad held-links query", detail)
	}
	window, limit := heldLinksDefaultWindow, heldLinksDefaultLimit
	q := r.URL.Query()
	if v := strings.TrimSpace(q.Get("since")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 || d > heldLinksMaxWindow {
			return 0, 0, bad("since must be a duration like 24h, at most 720h")
		}
		window = d
	}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > heldLinksMaxLimit {
			return 0, 0, bad("limit must be between 1 and 1000")
		}
		limit = n
	}
	return window, limit, nil
}
