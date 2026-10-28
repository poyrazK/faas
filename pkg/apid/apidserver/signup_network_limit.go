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
// address or network has created its daily share (errSignupNetworkCap).
func (s *server) createSignupAccount(r *http.Request, params state.CreateAccountWithPersonalOrgParams) (state.CreateAccountWithPersonalOrgResult, error) {
	ip := clientIPFromRequest(r)
	if !s.signups.allow(ip) {
		s.audit.Emit(r.Context(), "auth.signup_network_cap", nil, map[string]any{"ip": ip})
		return state.CreateAccountWithPersonalOrgResult{}, errSignupNetworkCap
	}
	res, err := s.store.CreateAccountWithPersonalOrg(r.Context(), params)
	if err == nil {
		s.signups.record(ip)
	}
	return res, err
}

func writeSignupNetworkCap(w http.ResponseWriter) {
	api.WriteProblem(w, api.ErrAuthRateLimited(int((24 * time.Hour).Seconds())))
}
