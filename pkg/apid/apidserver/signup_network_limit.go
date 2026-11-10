package apidserver

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// errSignupNetworkCap refuses an account creation from a network that has
// created its daily share of accounts.
var errSignupNetworkCap = errors.New("apidserver: sign-up cap reached for this network")

// signupNetworkLimiter caps the accounts one address and one network (an
// IPv4 /24 or IPv6 /48) create per day. The only sign-up throttle was 10
// attempts a minute per IP, about 14,000 accounts a day from one address, and
// disposable inboxes pass email verification, so a launch-day farm could turn
// the Free tier into free hosting. Per apid process.
type signupNetworkLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	window  time.Time
	created map[string]int
}

func newSignupNetworkLimiter() *signupNetworkLimiter {
	return &signupNetworkLimiter{now: time.Now, created: map[string]int{}}
}

func signupNetworkKeys(ip string) (addr, network string) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", ""
	}
	if v4 := parsed.To4(); v4 != nil {
		return "ip:" + v4.String(), "net:" + v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return "ip:" + parsed.String(), "net:" + parsed.Mask(net.CIDRMask(48, 128)).String()
}

func (l *signupNetworkLimiter) rollLocked() {
	if now := l.now(); now.Sub(l.window) >= 24*time.Hour {
		l.window = now
		clear(l.created)
	}
}

func (l *signupNetworkLimiter) allow(ip string) bool {
	addr, network := signupNetworkKeys(ip)
	if l == nil || addr == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	return l.created[addr] < api.SignupAccountsPerAddressPerDay && l.created[network] < api.SignupAccountsPerNetworkPerDay
}

func (l *signupNetworkLimiter) record(ip string) {
	addr, network := signupNetworkKeys(ip)
	if l == nil || addr == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	l.created[addr]++
	l.created[network]++
}

// createSignupAccount creates a self-service account unless the caller's
// address or network has created its daily share (errSignupNetworkCap). Every
// sign-up method passes here, Google and GitHub included (ADR-20261028205920),
// and each new account is counted by method and source.
func (s *server) createSignupAccount(r *http.Request, method, source string, params state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error) {
	ip := clientIPFromRequest(r)
	if !s.signups.allow(ip) {
		s.signupMetrics.observeCapped(signupCapNetwork)
		s.audit.Emit(r.Context(), "auth.signup_network_cap", nil, map[string]any{"ip": ip})
		return state.CreateAccountWithPersonalOrgResult{}, errSignupNetworkCap
	}
	if s.signupFleetCapped(r) {
		s.audit.Emit(r.Context(), "auth.signup_network_cap", nil, map[string]any{"ip": ip})
		return state.CreateAccountWithPersonalOrgResult{}, errSignupNetworkCap
	}
	// Sign-up accepts the published Terms of Service; the version is stored
	// with the account in one transaction (legal_acceptances).
	if params.TermsAcceptance == nil {
		params.TermsAcceptance = signupTermsAcceptance(method)
	}
	res, err := s.store.CreateAccountWithPersonalOrg(r.Context(), params)
	if err == nil {
		s.signups.record(ip)
		s.recordSignup(r.Context(), res.Account.ID, method, source)
	}
	return res, err
}

// signupNetworkCapped reports, before any account lookup, whether the
// caller's address or network has used its daily sign-up share. Password
// sign-up routes check it first so the capped answer is the same for bound
// and unbound emails (H97); createSignupAccount still enforces the cap at
// creation.
func (s *server) signupNetworkCapped(r *http.Request) bool {
	ip := clientIPFromRequest(r)
	networkOK := s.signups.allow(ip)
	if networkOK && !s.signupFleetCapped(r) {
		return false
	}
	if !networkOK {
		s.signupMetrics.observeCapped(signupCapNetwork)
	}
	s.audit.Emit(r.Context(), "auth.signup_network_cap", nil, map[string]any{"ip": ip})
	return true
}

func writeSignupNetworkCap(w http.ResponseWriter) {
	api.WriteProblem(w, api.ErrAuthRateLimited(int((24 * time.Hour).Seconds())))
}

// signupFleetCapped reports whether the platform created its daily share of
// accounts, api.SignupAccountsPerDay in the last 24 hours
// (ADR-20261104145353). Every new account carries a full Free allowance and
// the per-address caps reset with each new address, so this bounds what a
// signup farm can claim. A store without the count, or a failed read, admits:
// the per-address and per-network caps still apply.
func (s *server) signupFleetCapped(r *http.Request) bool {
	counter, ok := s.store.(state.SignupCountStore)
	if !ok {
		return false
	}
	n, err := counter.CountAccountsCreatedSince(r.Context(), timeNow().Add(-24*time.Hour))
	if err != nil {
		s.log.Warn("apid: sign-up fleet count failed; admitting", "err", err)
		return false
	}
	if n < api.SignupAccountsPerDay {
		return false
	}
	s.signupMetrics.observeCapped(signupCapFleet)
	s.audit.Emit(r.Context(), "auth.signup_fleet_cap", nil, map[string]any{"accounts_last_24h": n})
	return true
}
