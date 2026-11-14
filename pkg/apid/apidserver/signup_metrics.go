package apidserver

import (
	"context"
	"errors"
	"net/url"

	"github.com/prometheus/client_golang/prometheus"
)

// Sign-up methods and sources (ADR-20261028205920). The source says where a
// new account's sign-in was heading: an agent's MCP approval, a CLI approval,
// or nowhere in particular.
const (
	signupMethodPassword  = "password"
	signupMethodMagicLink = "magic_link"
	signupMethodGoogle    = "google"
	signupMethodGitHub    = "github"

	signupSourceAgent  = "agent"
	signupSourceCLI    = "cli"
	signupSourceDirect = "direct"
)

// signupMetrics counts new accounts by method and source, without account
// labels, so operators can see how many sign-ups agents bring.
type signupMetrics struct {
	registry prometheus.Registerer
	signups  *prometheus.CounterVec
	// capped counts sign-ups refused by the daily caps, by scope: one
	// address or network, or the whole platform (ADR 20261110120000).
	capped *prometheus.CounterVec
}

const (
	signupCapNetwork = "network"
	signupCapFleet   = "fleet"
)

func newSignupMetrics(reg prometheus.Registerer, prefix string) *signupMetrics {
	m := &signupMetrics{registry: reg, signups: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_signups_total",
		Help: "Accounts created by sign-up method and source: agent (continuing to an MCP approval), cli, or direct.",
	}, []string{"method", "source"})}
	if err := reg.Register(m.signups); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			panic(err)
		}
		m.signups = already.ExistingCollector.(*prometheus.CounterVec)
	}
	m.capped = registerCounterVec(reg, prometheus.CounterOpts{
		Name: prefix + "_signups_capped_total",
		Help: "Sign-ups refused by the daily caps, by scope: network (one address or network) or fleet (the platform's daily total).",
	}, []string{"scope"})
	for _, scope := range []string{signupCapNetwork, signupCapFleet} {
		m.capped.WithLabelValues(scope).Add(0)
	}
	for _, method := range []string{signupMethodPassword, signupMethodMagicLink, signupMethodGoogle, signupMethodGitHub} {
		for _, source := range []string{signupSourceAgent, signupSourceCLI, signupSourceDirect} {
			m.signups.WithLabelValues(method, source).Add(0)
		}
	}
	return m
}

func (m *signupMetrics) observeCapped(scope string) {
	if m != nil {
		m.capped.WithLabelValues(scope).Inc()
	}
}

// registerCounterVec registers c, or returns the collector already
// registered under its name.
func registerCounterVec(reg prometheus.Registerer, opts prometheus.CounterOpts, labels []string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(opts, labels)
	if err := reg.Register(c); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			panic(err)
		}
		return already.ExistingCollector.(*prometheus.CounterVec)
	}
	return c
}

func (m *signupMetrics) observe(method, source string) {
	if m != nil {
		m.signups.WithLabelValues(method, source).Inc()
	}
}

// signupSource classifies the validated path a sign-in continues to.
func signupSource(next string) string {
	u, err := url.Parse(next)
	if err != nil || u.Path != cliAuthPath || u.Query().Get("code") == "" {
		return signupSourceDirect
	}
	if isMCPAuthCode(u.Query().Get("code")) {
		return signupSourceAgent
	}
	return signupSourceCLI
}

// recordSignup counts a new account and records auth.signup on it.
func (s *server) recordSignup(ctx context.Context, accountID, method, source string) {
	s.signupMetrics.observe(method, source)
	s.audit.Emit(ctx, "auth.signup", &accountID, map[string]any{"method": method, "source": source})
}

// registerBackgroundMailDropped exports the security-mail pool's refusals:
// password reset, sign-in links, verification and notices that were never
// sent because the pool was full (ADR 20261110120000).
func registerBackgroundMailDropped(reg prometheus.Registerer, prefix string, pool *backgroundWork) {
	c := prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: prefix + "_background_mail_dropped_total",
		Help: "Security mails (password reset, sign-in links, verification, account notices) not sent because the background mail pool was full.",
	}, pool.droppedCount)
	if err := reg.Register(c); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			panic(err)
		}
	}
}
