// Package authz (loadorg.go) — LoadOrgWithResolver resolves the
// caller's active-org membership for the current request and stamps
// it onto the request's principal so downstream handlers + AuthorizeOrgAction
// see the same membership. This is half of the authz seam PR 4 of
// issue #190 / IAM-6 / ADR-061 ships alongside the AuthorizeOrgAction table.
//
// Header (X-Active-Org) wins over query (?org=), which wins over the
// route's {slug} path value. Neither set → pass-through.
// Unknown slug → 404 org_not_found; known org but caller is not a
// member → 403 org_role_forbidden (IDOR-safe — both 4xx so a
// non-member of an existing org sees the same shape as a non-member
// of a non-existent org, only the code differs). No principal on
// ctx → 500 CodeCapacity (wiring bug, not user error).
//
// Audit emission is opt-in via LoadOrgConfig.Audit; passing nil
// keeps the PR 4 hot path allocation-free. PR 5+ plumbs the real
// pkg/audit emitter through cmd/apid/main.go.
package authz

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// maxSlugLen is the upper bound LoadOrgWithResolver accepts on the
// incoming slug before looking it up. It is intentionally larger
// than the schema's 3..32 char CHECK (migration 00099) so any
// future widening of the schema does not invalidate this gate.
// The full regex lives in pkg/api.OrgSlugPattern; we only
// trim + length-cap here so an oversize / whitespace-only value
// degrades to passthrough rather than a 500 from the SQL CHECK.
const maxSlugLen = 64

// logHashShort returns a short, fixed-length, non-reversible
// fingerprint of an account id (or any opaque server-side
// identifier) for use in error logs. The output is a 16-hex-char
// prefix of the SHA-256 digest — 64 bits is plenty of entropy to
// disambiguate per-account log entries while staying short enough
// to read in a 1-line log line.
//
// The function name is load-bearing: CodeQL's CleartextLogging
// module treats any call whose callee name matches the
// notSensitive() regex (which includes `hash` / `sha` / `md5` /
// `redact` / `obfuscate`) as an obfuscator barrier, so data
// originating from an HTTP header or URL query parameter stops
// flowing at the call to logHashShort. This is the structural
// reason the load-org error-path Warn lines in this file do not
// raise go/clear-text-logging alerts: account_id is hashed before
// it reaches slog. Renaming this function (or inlining the hash
// computation) re-opens the alert.
//
// Returns "<hash:0>" for empty input so the field is always
// present in the JSON log output — empty values are ambiguous
// between "not set" and "literal empty string".
func logHashShort(s string) string {
	if s == "" {
		return "<hash:0>"
	}
	sum := sha256.Sum256([]byte(s))
	return "h:" + hex.EncodeToString(sum[:8])
}

// LoadOrgConfig configures LoadOrgWithResolver. The zero value is
// usable: header defaults to "X-Active-Org", query to "org", and
// the audit emitter is nil (no audit rows emitted).
type LoadOrgConfig struct {
	// Log is the structured logger; nil → slog.Default().
	Log *slog.Logger

	// HeaderName is the request header to read. Empty defaults to
	// "X-Active-Org" (ADR-061 wording).
	HeaderName string

	// QueryName is the query-string parameter to read. Empty
	// defaults to "org".
	QueryName string

	// Now is the clock function. nil defaults to time.Now. Tests
	// pass a fixed time so audit-row timestamps are stable.
	Now func() time.Time

	// Audit is the audit emitter for org-resolution failures.
	// nil → no audit rows emitted (PR 4 default; PR 5+ wires the
	// real pkg/audit seam).
	Audit AuditEmitter
}

// LoadOrgWithResolver is the canonical LoadOrg constructor. It
// mirrors pkg/middleware.AuthLimitWithLimiter's shape: panics on
// nil resolver, defaults the config, returns a func(next) Handler
// suitable for use with mux.Handle(...) and the cmd/apid façade
// pattern.
//
// The closure captures cfg + resolver; a per-request call site
// invokes the returned handler with the request lifecycle (httptest
// and the production mux both honour this).
func LoadOrgWithResolver(cfg LoadOrgConfig, r OrgResolver) func(http.Handler) http.Handler {
	if r == nil {
		// Same panic shape as pkg/middleware/authlimit.go. Refuse
		// to silently fall back to a nil-resolver no-op — that's
		// exactly the fail-open path this middleware exists to
		// prevent.
		panic("authz: LoadOrgWithResolver requires a non-nil OrgResolver")
	}
	if cfg.HeaderName == "" {
		cfg.HeaderName = "X-Active-Org"
	}
	if cfg.QueryName == "" {
		cfg.QueryName = "org"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			handleLoadOrg(cfg, r, w, req, next)
		})
	}
}

// handleLoadOrg is the per-request body, factored out so the tests
// can drive it directly with an httptest.ResponseRecorder and a
// stub OrgResolver.
func handleLoadOrg(cfg LoadOrgConfig, r OrgResolver, w http.ResponseWriter, req *http.Request, next http.Handler) {
	// (1) Recover the principal directly from the request.
	// RequireSession stamps it before LoadOrg runs (the cmd/apid
	// route table composes s.auth → s.loadOrg). If the principal
	// is missing, the route is mis-wired — fail closed.
	// We go through *http.Request (not ctx) because the principal
	// is keyed on the request, not on a ctx value that LoadOrg
	// itself stamps — anyone reading the principal at the top of
	// this function must read it from req, not from a ctx that's
	// only stamped at the bottom of this function.
	acct, _, _, ok := principalFromRequest(req)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"LoadOrg wired before RequireSession",
			"no principal on request; check the route table"))
		return
	}

	// (2) Read the slug from header (preferred) or query, then
	// trim + length-cap. Empty / whitespace-only / oversize →
	// passthrough (the SQL CHECK rejects oversize values with a
	// non-ErrNotFound error, which would otherwise surface as 500).
	slug := strings.TrimSpace(req.Header.Get(cfg.HeaderName))
	if slug == "" {
		slug = strings.TrimSpace(req.URL.Query().Get(cfg.QueryName))
	}
	if slug == "" {
		// Org-scoped routes already carry their authoritative slug in the
		// Go 1.22 ServeMux path value. Requiring every client to duplicate it
		// in X-Active-Org left the normal CLI path without a membership and
		// caused the downstream authorization gate to return a wiring 500.
		slug = strings.TrimSpace(req.PathValue("slug"))
	}
	if slug == "" || len(slug) > maxSlugLen {
		// Passthrough: no active org requested (or the hint is
		// malformed — header/query grabbers accept anything; the
		// schema is the only authority). Pre-PR-5 routes stay
		// account-scoped; the membership field on the principal
		// is left nil.
		next.ServeHTTP(w, req)
		return
	}

	// (3) Resolve the org.
	org, err := r.OrgBySlug(req.Context(), slug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			emitAudit(req.Context(), cfg.Audit, acct, "org.load.not_found", api.ErrOrgNotFound(slug))
			api.WriteProblem(w, api.ErrOrgNotFound(slug))
			return
		}
		// Note: this log line omits the slug on purpose. The slug
		// comes from the X-Active-Org header (or the ?org= query
		// parameter), and the CodeQL go/clear-text-logging rule
		// (CWE-200) flags any HTTP-header / URL-query source flowing
		// into a log call — strings.ReplaceAll closes go/log-injection
		// but the cleartext rule's barrier model only honours
		// header.Get for headers OTHER than Authorization / Cookie,
		// and url.Values.Get has no barrier at all. The slug is
		// already captured in the audit row above
		// (org.load.not_found → ErrOrgNotFound(slug)) and in the
		// emitAudit call below, so a missing-slug log line is the
		// structural fix. account_id is server-generated from the
		// session cookie (also a CodeQL-tracked header source) so
		// we hash it via logHashShort, which matches the
		// `notSensitive()` regex that CodeQL's CleartextLogging
		// module recognises as an obfuscator barrier. The
		// logsanitize.Field wrapper strips ASCII control chars
		// (defence-in-depth for NUL/DEL/CRLF on the hash output).
		// TestLoadOrg_LogSanitizesSlug pins the sanitization of the
		// slug even though the slug is no longer logged in the
		// org-lookup-failed path; the membership-lookup-failed
		// path below has the same defensive treatment.
		//
		// codeql[go/clear-text-logging] false-positive: logHashShort matches the rule's notSensitive() obfuscator regex.
		// codeql[go/log-injection] false-positive: logHashShort strips CR/LF/NUL/DEL; same root cause as the cert_expiry.go:333 case.
		cfg.Log.Warn("org lookup failed", "account_id_hash", logsanitize.Field(logHashShort(acct.ID)), "error", err.Error())
		emitAudit(req.Context(), cfg.Audit, acct, "org.load.error", api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Org lookup failed",
			"try again; if the problem persists, contact support"))
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Org lookup failed",
			"try again; if the problem persists, contact support"))
		return
	}

	// (4) Resolve the membership. Non-member → 403 IDOR-safe.
	mem, err := r.OrgMemberByAccount(req.Context(), org.ID, acct.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			emitAudit(req.Context(), cfg.Audit, acct, "org.load.not_member", api.ErrOrgRoleForbidden("access this organization"))
			api.WriteProblem(w, api.ErrOrgRoleForbidden("access this organization"))
			return
		}
		// codeql[go/clear-text-logging] false-positive: logHashShort matches the rule's notSensitive() obfuscator regex.
		// codeql[go/log-injection] false-positive: same root cause as the org-lookup-failed call site above; cert_expiry.go:333 has the same fix.
		cfg.Log.Warn("membership lookup failed", "org_id", logsanitize.Field(org.ID), "account_id_hash", logsanitize.Field(logHashShort(acct.ID)), "error", err.Error())
		emitAudit(req.Context(), cfg.Audit, acct, "org.load.error", api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Membership lookup failed",
			"try again; if the problem persists, contact support"))
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity,
			"Membership lookup failed",
			"try again; if the problem persists, contact support"))
		return
	}

	// (5) Replace the request context with the membership stamped.
	// The principal stays the same; only Membership changes.
	// Pointer-mutation contract (issue #278 / PR #332): we mutate
	// *req so downstream observeWrap (cmd/apid) sees the new
	// membership via MembershipFrom.
	//
	// Order matters: WithRequestOnContext must run BEFORE
	// WithActiveOrg so the latter can find the request via the
	// httpReqCtxKey{} stamp. WithActiveOrg falls back to a no-op
	// ctx otherwise (the principal is still on the request, but
	// the membership slot would not be updated).
	newCtx := WithRequestOnContext(req.Context(), req)
	newCtx = WithActiveOrg(newCtx, &mem)
	*req = *req.WithContext(newCtx)

	// (6) Continue.
	next.ServeHTTP(w, req)
}

// Compile-time guard: the constructor signature must stay
// assignable to func(http.Handler) http.Handler. If a future refactor
// accidentally widens it (e.g. adds an extra return value), this
// trips at build time.
var _ func(http.Handler) http.Handler = LoadOrgWithResolver(LoadOrgConfig{}, (*StoreBackedResolver)(nil))
