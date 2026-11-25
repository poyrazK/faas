package apidserver

import (
	"context"
	"errors"
	"net/url"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/apid/claim"
)

// Sign-up methods and sources (ADR-20261028205920). The source says where a
// new account's sign-in was heading: an agent's MCP approval, a CLI approval,
// or nowhere in particular.
const (
	signupMethodPassword  = "password"
	signupMethodMagicLink = "magic_link"
	signupMethodGoogle    = "google"
	signupMethodGitHub    = "github"
	// signupMethodAnonymous is an anonymous deploy's unclaimed account
	// (ADR-20261124080000).
	signupMethodAnonymous = "anonymous"

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
	// factsFailed counts sign-ups whose ADR-20261113160000 sign-up record
	// could not be stored; the account is created either way.
	factsFailed *prometheus.CounterVec
	// heldLinks counts new accounts flagged as resembling an abuse-held
	// account, by signal (ADR-20261114174600); "error" counts failed checks.
	heldLinks *prometheus.CounterVec
	// unclaimed counts claimable-deploy account events (ADR-20261124080000):
	// created, claimed, expired, and refusals by reason.
	unclaimed *prometheus.CounterVec
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
	m.factsFailed = registerCounterVec(reg, prometheus.CounterOpts{
		Name: prefix + "_signup_facts_failed_total",
		Help: "Sign-ups whose sign-up record (address, country, network owner, email domain) could not be stored; the account was created.",
	}, nil)
	m.factsFailed.WithLabelValues().Add(0)
	m.heldLinks = registerCounterVec(reg, prometheus.CounterOpts{
		Name: prefix + "_signup_held_links_total",
		Help: "New accounts linked to an abuse-held account for operator review, by signal: signup_network, email_domain, or error (the check failed).",
	}, []string{"signal"})
	for _, signal := range []string{"signup_network", "email_domain", "error"} {
		m.heldLinks.WithLabelValues(signal).Add(0)
	}
	m.unclaimed = registerCounterVec(reg, prometheus.CounterOpts{
		Name: prefix + "_unclaimed_accounts_total",
		Help: "Anonymous-deploy accounts by event: created, claimed, expired, or refused (refused_network, refused_live_cap, refused_held_link).",
	}, []string{"event"})
	for _, event := range claim.Events {
		m.unclaimed.WithLabelValues(event).Add(0)
	}
	for _, scope := range []string{signupCapNetwork, signupCapFleet} {
		m.capped.WithLabelValues(scope).Add(0)
	}
	m.signups.WithLabelValues(signupMethodAnonymous, signupSourceAgent).Add(0)
	for _, method := range []string{signupMethodPassword, signupMethodMagicLink, signupMethodGoogle, signupMethodGitHub} {
		for _, source := range []string{signupSourceAgent, signupSourceCLI, signupSourceDirect} {
			m.signups.WithLabelValues(method, source).Add(0)
		}
	}
	return m
}

func (m *signupMetrics) observeHeldLink(signal string) {
	if m != nil {
		m.heldLinks.WithLabelValues(signal).Inc()
	}
}

func (m *signupMetrics) observeUnclaimed(event string) {
	if m != nil && m.unclaimed != nil {
		m.unclaimed.WithLabelValues(event).Inc()
	}
}

func (m *signupMetrics) observeFactsFailed() {
	if m != nil {
		m.factsFailed.WithLabelValues().Inc()
	}
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
