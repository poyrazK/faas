// Package claim serves claimable deploys (ADR-20261124080000).
//
// An agent deploys with no account. POST /v1/anonymous/sessions creates a
// Free account with a placeholder address in the reserved .invalid domain,
// records it in unclaimed_accounts with an expiry, and returns a deploy key
// and a claim link. Until it is claimed the account may only deploy and read
// its apps (RouteAllowed). A human claims it by opening the link and proving
// an email address; the account then becomes theirs in place, with its apps,
// and the anonymous key stops working. An unclaimed account past its expiry
// is scheduled for deletion like any other.
//
// apid's server adapts itself to Host; this package holds the handlers, the
// claim pages, the route policy and the expiry sweep.
package claim

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// Capability owns the anonymous session and claim routes in the
	// product catalog. Anonymous sessions are offered only once its flag is
	// on by default.
	Capability = "claimable-deploys"

	// KeyLabel names the deploy key an anonymous session mints.
	KeyLabel = "anonymous-deploy"
	// TokenBytes is the claim link's random token length.
	TokenBytes = 32

	// PagePath and ConfirmPath are the claim pages on the API host.
	PagePath    = "/claim"
	ConfirmPath = "/claim/confirm"
)

// Events counted by apid_unclaimed_accounts_total{event}.
const (
	EventCreated         = "created"
	EventClaimed         = "claimed"
	EventExpired         = "expired"
	EventRefusedNetwork  = "refused_network"
	EventRefusedLiveCap  = "refused_live_cap"
	EventRefusedHeldLink = "refused_held_link"
)

// Events lists every event so the metric can be pre-initialised.
var Events = []string{
	EventCreated, EventClaimed, EventExpired,
	EventRefusedNetwork, EventRefusedLiveCap, EventRefusedHeldLink,
}

// Settings are the operator's hot limits for anonymous deploys.
type Settings struct {
	TTL        time.Duration
	LiveMax    int
	PerAddress int
	PerNetwork int
}

// Host is what claimable deploys need from apid.
type Host interface {
	Store() state.Store
	Log() *slog.Logger
	// Offered reports whether anonymous sessions are offered at all.
	Offered() bool
	Settings() Settings
	ClientIP(r *http.Request) string
	// Observe counts one event (Events).
	Observe(event string)
	Audit(ctx context.Context, event string, accountID *string, fields map[string]any)

	// TermsAcceptance is the Terms row an anonymous account is created with.
	TermsAcceptance() *state.TermsAcceptance
	// RecordSignup counts the new account as an agent sign-up.
	RecordSignup(ctx context.Context, accountID string)
	// RecordSignupFacts stores the sign-up facts and returns how many
	// abuse-held accounts the new one resembles.
	RecordSignupFacts(ctx context.Context, r *http.Request, accountID, email string) int
	RecordKeyDisplayPrefix(ctx context.Context, keyID, plaintext string)
	// ScheduleDeletion schedules the account's deletion like a customer's
	// own request.
	ScheduleDeletion(ctx context.Context, acct state.Account, via string) *api.Problem

	// ClaimBase is the https origin claim pages are served from.
	ClaimBase(r *http.Request) string
	// LoginURL is the sign-in page with query appended.
	LoginURL(r *http.Request, query string) string
	// MagicLinkURL is the one-time sign-in link for a login token.
	MagicLinkURL(r *http.Request, token string) string
	// AppURL is an app's public URL.
	AppURL(slug string) string

	AllowSecurityMail(ctx context.Context, acct state.Account, kind string) bool
	SendMail(ctx context.Context, to, subject, body string) error
	// Background runs fn off the request with a bounded number in flight;
	// false when it was dropped.
	Background(fn func(ctx context.Context)) bool
	// FinishEmailChange runs apid's bookkeeping after an email change; for
	// a placeholder address it calls Service.FinishClaim.
	FinishEmailChange(r *http.Request, change state.EmailChange)
}

// Service serves claimable deploys. Now nil uses time.Now.
type Service struct {
	Host Host
	Now  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) unclaimedStore() (state.UnclaimedAccountStore, bool) {
	store, ok := s.Host.Store().(state.UnclaimedAccountStore)
	return store, ok
}

// IsUnclaimed reports whether acct is an unclaimed anonymous-deploy
// account. The placeholder address is the marker: only an anonymous session
// creates one, and the claim replaces it, so no store read is needed.
func IsUnclaimed(acct state.Account) bool {
	return api.IsUnclaimedPlaceholderEmail(acct.Email)
}

// Limits describes what an unclaimed account may use.
func Limits() api.UnclaimedLimits {
	limits, _ := api.LimitsFor(api.PlanFree)
	return api.UnclaimedLimits{
		Plan:    string(api.PlanFree),
		MaxApps: limits.DeployedApps,
		MaxRAM:  limits.RAMMB,
		Blocked: append([]string(nil), api.UnclaimedBlockedFeatures...),
	}
}

func newToken(n int) ([]byte, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeToken(s string, n int) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != n {
		return nil, false
	}
	return raw, true
}

func (s *Service) claimURL(r *http.Request, raw []byte) string {
	return s.Host.ClaimBase(r) + PagePath + "/" + base64.RawURLEncoding.EncodeToString(raw)
}

func truncateClient(client string) string {
	client = strings.TrimSpace(client)
	if len(client) > 80 {
		client = client[:80]
	}
	return client
}
