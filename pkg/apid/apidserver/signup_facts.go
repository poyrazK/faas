package apidserver

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

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
// already exists, so a failure is logged and counted, never returned.
func (s *server) recordSignupFacts(ctx context.Context, r *http.Request, accountID, email, method string) {
	store, ok := s.store.(state.AccountSignupFactsStore)
	if !ok {
		return
	}
	facts := s.signupGeo.signupFacts(accountID, email, method, clientIPFromRequest(r), r.UserAgent(), timeNow())
	if err := store.RecordAccountSignupFacts(context.WithoutCancel(ctx), facts); err != nil {
		s.log.Warn("apid: signup facts not recorded", "account", accountID, "err", err)
		s.signupMetrics.observeFactsFailed()
	}
}
