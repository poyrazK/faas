package apidserver

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/disposableemail"
	"github.com/onebox-faas/faas/pkg/geoip"
	"github.com/onebox-faas/faas/pkg/state"
)

// Default database paths; they match gatewayd-internal's and the geoip
// Ansible role, which stages both files on the control plane too.
const (
	signupGeoCountryDefaultPath = "/var/lib/faas/geoip/dbip-country-lite.mmdb"
	signupGeoASNDefaultPath     = "/var/lib/faas/geoip/dbip-asn-lite.mmdb"
)

// signupGeo resolves a sign-up address to its country and network owner from
// the local DB-IP Lite databases (ADR-20261113160000). Either lookup may be
// nil: the fact is then left empty, and sign-up never depends on it.
type signupGeo struct {
	country func(net.IP) string
	asn     func(net.IP) (uint32, string)
}

// openSignupGeo opens the databases at FAAS_GEOIP_DB_PATH and
// FAAS_GEOIP_ASN_DB_PATH (gatewayd-internal's variables and defaults). A
// missing file is normal off the control plane and leaves that lookup nil.
func openSignupGeo(getenv func(string) string, log *slog.Logger) *signupGeo {
	path := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
	open := func(p, what string) *geoip.Reader {
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			log.Info("apid: signup "+what+" database not found; the fact stays empty", "path", p)
			return nil
		}
		r, err := geoip.Open(p, geoip.SourceDBIP, geoip.DBIPAttribution, log)
		if err != nil {
			log.Warn("apid: signup "+what+" database failed to open; the fact stays empty", "path", p, "err", err)
			return nil
		}
		return r
	}
	g := &signupGeo{}
	if r := open(path("FAAS_GEOIP_DB_PATH", signupGeoCountryDefaultPath), "country"); r != nil {
		g.country = func(ip net.IP) string {
			c, ok, err := r.Lookup(ip)
			if err != nil || !ok {
				return ""
			}
			return c
		}
	}
	if r := open(path("FAAS_GEOIP_ASN_DB_PATH", signupGeoASNDefaultPath), "network owner"); r != nil {
		g.asn = func(ip net.IP) (uint32, string) {
			n, org, ok, err := r.LookupASN(ip)
			if err != nil || !ok {
				return 0, ""
			}
			return n, org
		}
	}
	return g
}

// signupFacts builds the sign-up record for a new account.
func (g *signupGeo) signupFacts(accountID, email, method, ip, userAgent string, at time.Time) state.AccountSignupFacts {
	domain := disposableemail.EmailDomain(email)
	f := state.AccountSignupFacts{
		AccountID: accountID, Method: method, UserAgent: userAgent,
		EmailDomain: domain, DisposableEmail: disposableemail.Domain(domain), CreatedAt: at,
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return f
	}
	f.SignupIP = parsed.String()
	if g != nil && g.country != nil {
		f.Country = g.country(parsed)
	}
	if g != nil && g.asn != nil {
		f.ASN, f.ASNOrg = g.asn(parsed)
	}
	return f
}

// recordSignupFacts stores the sign-up record. It is best effort: the account
// already exists, so a failure is logged and counted, never returned. It
// returns how many abuse-held accounts the new one was linked to.
func (s *server) recordSignupFacts(ctx context.Context, r *http.Request, accountID, email, method string) int {
	store, ok := s.store.(state.AccountSignupFactsStore)
	if !ok {
		return 0
	}
	facts := s.signupGeo.signupFacts(accountID, email, method, clientIPFromRequest(r), r.UserAgent(), timeNow())
	ctx = context.WithoutCancel(ctx)
	if err := store.RecordAccountSignupFacts(ctx, facts); err != nil {
		s.log.Warn("apid: signup facts not recorded", "account", accountID, "err", err)
		s.signupMetrics.observeFactsFailed()
		return 0
	}
	return s.linkSignupToHeld(ctx, accountID, facts.EmailDomain)
}

// linkSignupToHeld flags a new account that resembles an abuse-held one
// (ADR-20261114174600): same /24 or /48 as the held account's sign-up or a
// sign-in within api.SignupFactsPersonalRetention, or the same private email
// domain. Flag only: each link is audited as accounts.linked_to_held and
// counted, and the new account is not held or refused.
func (s *server) linkSignupToHeld(ctx context.Context, accountID, emailDomain string) int {
	store, ok := s.store.(state.AccountHeldLinkStore)
	if !ok {
		return 0
	}
	now := timeNow()
	// Every unclaimed anonymous-deploy account shares the placeholder
	// domain (ADR-20261124080000): linking on it would tie each new one to
	// any held one, so only the network signals apply to them.
	domainLinkable := disposableemail.Linkable(emailDomain) && !strings.EqualFold(emailDomain, api.UnclaimedEmailDomain)
	links, err := store.LinkAccountToHeld(ctx, accountID, domainLinkable, now.Add(-api.SignupFactsPersonalRetention), now)
	if err != nil {
		s.log.Warn("apid: held-account linking failed", "account", accountID, "err", err)
		s.signupMetrics.observeHeldLink("error")
		return 0
	}
	for _, l := range links {
		detail := map[string]any{"held_account": l.HeldAccountID, "held_reason": l.HeldReason, "signal": l.Signal}
		if l.HeldOrgID != "" {
			detail["held_org"] = l.HeldOrgID
		}
		s.audit.Emit(ctx, "accounts.linked_to_held", &accountID, detail)
		s.signupMetrics.observeHeldLink(l.Signal)
	}
	if len(links) > 0 {
		s.log.Warn("apid: new account resembles an abuse-held account; flagged for review",
			"account", accountID, "links", len(links), "first_held_account", links[0].HeldAccountID, "first_signal", links[0].Signal)
	}
	return len(links)
}
