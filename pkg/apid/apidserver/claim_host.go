package apidserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/claim"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hot runtime settings of claimable deploys (ADR-20261124080000).
const (
	runtimeConfigUnclaimedTTLHours   = "unclaimed_deploy_ttl_hours"
	runtimeConfigUnclaimedLiveMax    = "unclaimed_accounts_live_max"
	runtimeConfigUnclaimedPerAddress = "unclaimed_accounts_per_address_per_day"
	runtimeConfigUnclaimedPerNetwork = "unclaimed_accounts_per_network_per_day"
)

// claimHost adapts *server to claim.Host (ADR-982, ADR-20261124080000).
type claimHost struct{ s *server }

var _ claim.Host = claimHost{}

func (h claimHost) Store() state.Store { return h.s.store }
func (h claimHost) Log() *slog.Logger  { return h.s.log }

// Offered: the capability flag is on for callers no rule names. A server
// without a gate (unit tests) offers them.
func (h claimHost) Offered() bool {
	return h.s.capGate == nil || h.s.capGate.ReleasedByDefault(claim.Capability)
}

func (h claimHost) Settings() claim.Settings {
	return claim.Settings{
		TTL:        time.Duration(h.s.runtimeInt(runtimeConfigUnclaimedTTLHours, api.UnclaimedDeployTTLHoursDefault)) * time.Hour,
		LiveMax:    h.s.runtimeInt(runtimeConfigUnclaimedLiveMax, api.UnclaimedAccountsLiveMaxDefault),
		PerAddress: h.s.runtimeInt(runtimeConfigUnclaimedPerAddress, api.UnclaimedAccountsPerAddressPerDayDefault),
		PerNetwork: h.s.runtimeInt(runtimeConfigUnclaimedPerNetwork, api.UnclaimedAccountsPerNetworkPerDayDefault),
	}
}

func (h claimHost) ClientIP(r *http.Request) string { return clientIPFromRequest(r) }
func (h claimHost) Observe(event string)            { h.s.signupMetrics.observeUnclaimed(event) }

func (h claimHost) Audit(ctx context.Context, event string, accountID *string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, accountID, fields)
}

func (h claimHost) TermsAcceptance() *state.TermsAcceptance {
	return signupTermsAcceptance(signupMethodAnonymous)
}

func (h claimHost) RecordSignup(ctx context.Context, accountID string) {
	h.s.recordSignup(ctx, accountID, signupMethodAnonymous, signupSourceAgent)
}

func (h claimHost) RecordSignupFacts(ctx context.Context, r *http.Request, accountID, email string) int {
	return h.s.recordSignupFacts(ctx, r, accountID, email, signupMethodAnonymous)
}

func (h claimHost) RecordKeyDisplayPrefix(ctx context.Context, keyID, plaintext string) {
	h.s.recordKeyDisplayPrefix(ctx, keyID, plaintext)
}

func (h claimHost) ScheduleDeletion(ctx context.Context, acct state.Account, via string) *api.Problem {
	_, prob := h.s.scheduleDeletion(ctx, acct, via, false)
	return prob
}

// ClaimBase is the API host, which always routes /claim to apid (the apex
// is the web dashboard's).
func (h claimHost) ClaimBase(r *http.Request) string {
	if domain := strings.TrimPrefix(strings.TrimSpace(h.s.platformHost()), "."); domain != "" && domain != domainUnset {
		return "https://api." + domain
	}
	return h.s.passwordResetLinkBase(r)
}

func (h claimHost) LoginURL(r *http.Request, query string) string {
	return h.s.passwordResetLinkBase(r) + loginPath + "?" + query
}

func (h claimHost) MagicLinkURL(r *http.Request, token string) string {
	return h.s.passwordResetLinkBase(r) + magicLinkPath + "?token=" + token
}

func (h claimHost) AppURL(slug string) string {
	return appURLForDomain(slug, h.s.domain)
}

func (h claimHost) AllowSecurityMail(ctx context.Context, acct state.Account, kind string) bool {
	return h.s.allowSecurityMail(ctx, acct, kind)
}

func (h claimHost) SendMail(ctx context.Context, to, subject, body string) error {
	return h.s.mailer.Send(ctx, Message{To: []string{to}, Subject: subject, TextBody: body})
}

// Background runs fn on the forgot-password mail workers, with the
// request's values but its own timeout.
func (h claimHost) Background(fn func(ctx context.Context)) bool {
	return h.s.resetMail.try(func() {
		ctx, cancel := context.WithTimeout(context.Background(), api.PasswordResetMailTimeout)
		defer cancel()
		fn(ctx)
	})
}

func (h claimHost) FinishEmailChange(r *http.Request, change state.EmailChange) {
	h.s.finishEmailChange(r, change)
}

// unclaimedAllowed holds an unclaimed account to deploying and reading its
// apps (claim.RouteAllowed), and refuses an anonymous deploy key once its
// account has been claimed.
func (s *server) unclaimedAllowed(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		actor, key := actorAccount(r, acct), (*state.APIKey)(nil)
		if _, k, ok := authmw.AccountFromContext(r); ok {
			key = k
		}
		switch {
		case claim.IsUnclaimed(actor):
			if !claim.RouteAllowed(r.Pattern) || claim.IsUpgrade(r) {
				api.WriteProblem(w, api.ErrClaimRequired())
				return
			}
			if claim.FieldGated(r.Pattern) {
				if prob := refusedUnclaimedField(r); prob != nil {
					api.WriteProblem(w, prob)
					return
				}
			}
		case s.claim.EndedAnonymousKey(r, actor, key):
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Anonymous deploy key ended", "this app was claimed; sign in as its owner with gregale login"))
			return
		}
		next(w, r, acct)
	}
}

// unclaimedFieldBodyMax bounds the body read to check a field-gated route.
const unclaimedFieldBodyMax = 1 << 20

// refusedUnclaimedField reads the body of a field-gated route, puts it back
// for the handler and refuses a field an unclaimed account may not set.
func refusedUnclaimedField(r *http.Request) *api.Problem {
	body, err := io.ReadAll(io.LimitReader(r.Body, unclaimedFieldBodyMax+1))
	if err != nil || len(body) > unclaimedFieldBodyMax {
		return api.ErrValidation("request body is unreadable or too large")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	field := claim.RefusedField(r.Pattern, body)
	if field == "" {
		return nil
	}
	prob := api.ErrClaimRequired()
	prob.Detail = "setting " + field + " needs a claimed account. Open the claim link (gregale claim prints it) to sign up; until then a deploy may change only public access, protocol, size and restart settings."
	return prob
}
