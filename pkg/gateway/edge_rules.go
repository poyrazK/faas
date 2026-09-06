package gateway

// Edge Rules matcher surface (ADR-091 / issue #561, PR 3 + PR 4 + PR 5).
//
// PR 1 (#799) shipped the `edge_rules` table, state layer, apid CRUD,
// SDK, and OpenAPI. PR 2 (#815) shipped the `gregale edge-rules` CLI.
// PR 3 (#820) shipped the per-host LRU mirror of `RouteCache`
// (`pkg/gateway/routes.go`) holding the subset of each rule the matcher
// reads, plus two narrow interfaces the handler uses to inject
// `kind=route` substitution between the apps-suffix gate and
// `Backend.Lookup` at `pkg/gateway/handler.go:1609-1618`.
//
// PR 4 widens the matcher with three header-path kinds (rewrite /
// redirect / headers) — same forward-compatible interface shape: a
// `noOpEdgeRuleMatcher` embedded by future kinds gives the default
// no-op for free. The cache primitive widens to a per-host `hostEntry`
// that carries all four kinds' compiled slices; recompilation happens
// once on a miss for any kind (the SQL roundtrip dominates, so paying
// the compile cost four times is irrelevant).
//
// PR 5 adds three security-gate kinds (cors / jwt / ip) on top of the
// same surface — the cache widens to a 7-slot HostEntry and
// EdgeRuleMatcher adds MatchCORS / MatchJWT / MatchIP. The cache +
// invalidation plumbing stays unchanged.
//
// PR-B (issue #678, ADR follow-on) widens with an 8th kind, `validate`:
// a JSON-Schema body gate that runs BEFORE the wake gate fires. The
// 7-slot HostEntry widens to 8; the matcher widens with MatchValidate.
// The compiled-schema cache lives in pkg/edgevalidate (separate
// LRU keyed by SHA-256 of the raw schema bytes); this file only
// carries the resolver subset (SchemaDigest) and delegates Validate
// to pkg/edgevalidate.Validator via the cmd-side adapter.
//
// Why a subset type (not `state.EdgeRule`): pkg/gateway has no
// `pkg/state` import today and adding one would be a reverse dep
// (mirrors the existing `RequireAuthnAuthenticator` interface
// pattern at `pkg/gateway/handler.go:194-204`). The loader in
// `cmd/gatewayd-internal/edge_rules.go` is the single seam where
// `state.EdgeRule → EdgeRule*Resolved` happens.

import (
	"container/list"
	"context"
	"errors"
	"net"
	"path"
	"sync"
	"time"
)

// pathMatch is the stdlib path.Match wrapper — aliased here so the
// path-glob filter unit tests can stub it via build tags in the
// future without changing the production call site. Today it is a
// straight passthrough; the indirection documents the seam.
var pathMatch = path.Match

// pathGlobError is the parse-failure tuple the loader threads back
// to the gateway hot path so an operator can diagnose a malformed
// glob via slog. Lives in pkg/gateway (rather than cmd-side) so
// the cache entry shape carries it directly. The cmd-side loader
// (cmd/gatewayd-internal/edge_rules.go) imports this type and
// populates the hostEntry.pathGlobErrs slice.
//
// The rule itself is silently dropped from the compiled slice —
// the customer sees a 404 (no match), which is the safest outcome
// (a malformed rule firing would steer traffic unpredictably).
// PR 3 review-fix R3 introduced this so the typo is operator-visible.
type pathGlobError struct {
	RuleID string
	Glob   string
	Err    error
}

// PathGlobError is the exported alias of pathGlobError so the
// cmd-side loader (which lives in a different package) can
// populate hostEntry.PathGlobErrs. Mirrors the unexported
// type's fields verbatim; the alias keeps the package-private
// discipline intact (the slice is constructed in pkg/gateway
// via cmd-side, but the type lives in pkg/gateway to avoid
// a circular import).
type PathGlobError = pathGlobError

// EdgeRuleCacheCap is the maximum number of host entries kept in
// the in-memory LRU. Mirrors the spec §4.1 routing-cache capacity
// (10,000). Single-box scale makes wholesale Reset() cheaper than
// per-host tracking; gatewayd-internal calls Reset() on
// `db.NotifyEdgeRuleChanged`.
const EdgeRuleCacheCap = 10_000

// EdgeRuleConsumerCacheCap (ADR-104, issue #881 Phase 3) is the
// maximum number of per-rule per-consumer buckets the
// routeConsumerLimiter retains. Sized 10x the per-rule cap because
// each per-rule scope can carry up to MaxKeysPerRule (plan ceiling
// Scale 10_000) consumer buckets plus one pinned __other__ collapse
// bucket per rule. The full-bucket-only invariant from
// pkg/gateway/ratelimit.go:234-267 means the map may overshoot
// under sustained pressure; BucketCount surfaces the overshoot to
// /metrics. The default 100_000 covers the worst-case legit
// traffic (10k rules × 10 keys + 10k collapse buckets) with headroom
// for the LRU scan to actually find an evictable bucket.
const EdgeRuleConsumerCacheCap = 100_000

// reasonOther (issue #975 #3 / Mega-Foundation #979-a) is the
// overflow bucket for both EdgeValidateFieldError.Reason() and the
// gateway_edge_rule_validate_failures_total metric coercer. Hoisted
// from a string literal so goconst stops flagging the package-wide
// 6-occurrence duplication (across edge_rules.go + metrics.go +
// handler.go). The metric is a closed-set: changing this constant
// changes the wire-level label value on the metric, which the §12
// dashboard pins. See ValidateMode* constants for the mode side.
const reasonOther = "other"

// EdgeRuleResolved is the gateway-side subset of `state.EdgeRule`
// the matcher reads on every request. Fields are the minimum needed
// to (a) find a rule whose host matches, (b) apply the path/methods
// filter in Go, (c) resolve the target app via the closure injected
// at `WithEdgeRules`, (d) emit the audit row. Action JSON, full
// AppID, deployment data, and timestamps are intentionally absent —
// PR 4-7's per-kind actions read them out of `state.EdgeRule` again
// at the kind-specific code path.
type EdgeRuleResolved struct {
	ID            string
	AccountID     string
	AppID         string
	Priority      int
	PathGlob      string          // compiled via path.Match; "" = any path
	Methods       map[string]bool // empty = any method
	TargetAppSlug string          // kind=route only; ignored by PR 4-7
}

// EdgeRuleRewriteResolved is the kind=rewrite subset. PR 4 mutates
// r.URL.Path in place when a rule matches (From prefix → To
// replacement; the spec §4.1.2 documents the "$1" capture shape
// for trailing-`*` From patterns — applied via stdlib path.Match
// + string replace at filter time).
type EdgeRuleRewriteResolved struct {
	ID        string
	AccountID string
	AppID     string
	Priority  int
	PathGlob  string          // "" = any path
	Methods   map[string]bool // nil = any method
	From      string          // literal prefix to strip; "" = match-any
	To        string          // replacement prefix; required when rule fires
}

// EdgeRuleRedirectResolved is the kind=redirect subset. PR 4 emits
// a 3xx via http.Redirect (stdlib) when a rule matches. StatusCode
// ∈ {301,302,307,308}; the loader defaults to 302 when 0. Headers
// are stamped on the response via w.Header().Set before the redirect.
type EdgeRuleRedirectResolved struct {
	ID         string
	AccountID  string
	AppID      string
	Priority   int
	PathGlob   string
	Methods    map[string]bool
	StatusCode int
	To         string
	Headers    map[string]string
}

// EdgeRuleHeaderOp is one mutation a kind=headers rule carries.
// Action ∈ {add, set, remove}; Value is ignored for "remove".
// Blacklist (Host, Content-Length, Transfer-Encoding, Connection,
// x-faas-*) is enforced at apid-Validate-time (PR 1).
type EdgeRuleHeaderOp struct {
	Name   string
	Value  string
	Action string
}

// EdgeRuleHeadersResolved is the kind=headers subset. PR 4 applies
// RequestHeaders to r BEFORE the proxy leg and ResponseHeaders to
// w BEFORE w.WriteHeader (mirrors the existing statusRecorder order).
// Ops apply in declared order (Cloudflare's "first wins" rule for
// `set`); the order is preserved from the customer's wire input.
type EdgeRuleHeadersResolved struct {
	ID              string
	AccountID       string
	AppID           string
	Priority        int
	PathGlob        string
	Methods         map[string]bool
	RequestHeaders  []EdgeRuleHeaderOp
	ResponseHeaders []EdgeRuleHeaderOp
}

// EdgeRuleCORSResolved is the kind=cors subset (ADR-091). PR 5
// applies two branches in handler.go::applyEdgeRuleCORS:
//
//   - OPTIONS + this rule → 204 with Access-Control-Allow-* headers
//     stamped, no Backend.Lookup (preflight short-circuit).
//   - any method + this rule → stamps response-side headers via
//     statusRecorder (Access-Control-Allow-Origin echoes the
//     request Origin, ExposeHeaders, conditional AllowCredentials)
//     and falls through to Backend.Lookup.
//
// The validator (pkg/api/dto.go::EdgeRuleCORSAction.Validate,
// ADR-091 D12) rejects AllowOrigins:["*"] + AllowCredentials:true
// at create-time so the gateway stamper can trust the input
// shape.
type EdgeRuleCORSResolved struct {
	ID               string
	AccountID        string
	AppID            string
	Priority         int
	PathGlob         string
	Methods          map[string]bool
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAgeSeconds    int
	// PresetID (issue #975 #4 PR-B / ADR-129 D3) is the
	// resolved preset's id when the rule stamped a
	// cors_preset_id; empty when the rule is inline-only.
	// Stamped at compile time so the runtime applier
	// (handler.go::applyEdgeRuleCORS) can stamp the
	// Access-Control-* headers without a second lookup.
	PresetID string
}

// EdgeRuleJWTResolved is the kind=jwt subset (ADR-091). PR 5 calls
// pkg/edgejwks.Verifier.Verify with the raw token; the verifier
// fetches the JWKS via the per-rule URL, validates iss/aud/alg/
// required_claims with a 60s clock-skew tolerance, and returns
// typed errors so the applier can pick the right audit kind.
//
// JWKSURL is already https:// AND not RFC1918/loopback/link-local
// per the apid-Validate guard (ADR-091 D10). Algorithms is the
// closed {RS,ES}{256,384,512} vocabulary (HS* dropped — D11).
type EdgeRuleJWTResolved struct {
	ID             string
	AccountID      string
	AppID          string
	Priority       int
	PathGlob       string
	Methods        map[string]bool
	Issuer         string
	Audience       []string          // empty = skip aud check
	JWKSURL        string            // already https:// + not private
	Algorithms     []string          // closed vocab
	RequiredClaims map[string]string // key=value
}

// EdgeRuleIPResolved is the kind=ip subset (ADR-091). PR 5 calls
// applyEdgeRuleIP with the client IP extracted from the single
// trusted XFF entry (gatewayd-public's XFF injection at
// pkg/gateway/internal_proxy.go:286-288).
//
// Allow / Deny are parsed *net.IPNet slices. The compile side
// re-parses each CIDR string at compile time (mirrors PR 3 R3
// path-glob re-validation) and drops malformed ones with a
// parse error — apid-Validate already calls net.ParseCIDR once,
// but the SQL hotfix path means we can't trust the validator.
type EdgeRuleIPResolved struct {
	ID        string
	AccountID string
	AppID     string
	Priority  int
	PathGlob  string
	Methods   map[string]bool
	Allow     []*net.IPNet // nil = no allowlist
	Deny      []*net.IPNet // nil = no denylist
}

// EdgeRuleValidateResolved is the kind=validate subset (PR-B).
// The applier (handler.go::applyEdgeRuleValidate) buffers the
// inbound request body up to MaxBodyBytes (or api.MaxRequestBodyBytes
// if MaxBodyBytes ≤ 0), restores r.Body so the proxy leg still
// reads it, and consults pkg/edgevalidate.Validator with the
// resolved SchemaDigest. The compiled *CompiledSchema lives in
// pkg/edgevalidate.Cache keyed by SHA-256 — pkg/gateway doesn't
// import pkg/edgevalidate, mirroring the JWT seam at line 528.
//
// ContentTypes gates the request's Content-Type against a closed
// vocabulary (apid-Validate enforces "must start with application/"
// upstream). When nil/empty, any Content-Type passes (back-compat
// with rules that pre-date the field).
//
// ApplyWhileStreaming mirrors the per-rule apply_while_streaming
// knob: when the inbound request is part of a streaming response
// (the upgrade check at handler.go), the applier short-circuits
// to pass-through unless this is true. Body validation needs the
// full body, which streaming doesn't have.
type EdgeRuleValidateResolved struct {
	ID                  string
	AccountID           string
	AppID               string
	Priority            int
	PathGlob            string
	Methods             map[string]bool
	SchemaDigest        [32]byte // SHA-256 of the raw schema body
	ContentTypes        []string // nil/empty = any Content-Type
	ApplyWhileStreaming bool     // default false
	RejectUnknownFields bool     // audit-tag-only; schema-side authoritative
	MaxBodyBytes        int      // 0 = use api.MaxRequestBodyBytes
	// ValidateMode (issue #975 #3 / Mega-Foundation #979-a) is the
	// per-rule validate_mode (observe|warn|block). Default empty
	// string is treated as 'block' by the handler to match the
	// schema default (migrations/00293_validate_mode.sql). The
	// adapter fills this from the edge_rules.validate_mode column
	// at rule load; the value is read by the handler at the
	// schema-mismatch branch to decide whether to 422, pass-through,
	// or pass-through with the X-Validation-Warning header.
	ValidateMode string
}

// EdgeRuleGeoResolved is the kind=geo subset (ADR-091 D21/D22).
// Mirrors EdgeRuleIPResolved's shape exactly so the §4.1.2.8b
// matcher can share the same compile-side + path/methods filter
// helpers. The difference isAllow / Deny carry ISO 3166-1 alpha-2
// country codes as a string-keyed set, not CIDR networks.
//
// apid-Validate already drops reserved codes (AA, ZZ, etc.) before
// the row lands in PG, so the gateway can trust the wire shape
// here. The compile side uppercases and dedupes one more time
// for defense-in-depth (the §11 spirit — abuse gates must not
// hinge on a single validator's correctness).
type EdgeRuleGeoResolved struct {
	ID        string
	AccountID string
	AppID     string
	Priority  int
	PathGlob  string
	Methods   map[string]bool
	Allow     map[string]struct{} // ISO 3166-1 alpha-2 country codes; nil = no allowlist
	Deny      map[string]struct{} // ISO 3166-1 alpha-2 country codes; nil = no denylist
}

// EdgeRuleCache is the in-memory per-host LRU (PR 3 shape; PR 4
// widens the entry to a HostEntry carrying four compiled slices —
// one per kind). Wholesale `Reset()` on `db.NotifyEdgeRuleChanged`
// is the only invalidation — single-box scale assumption per
// spec §4.3. Mirrors `RouteCache` at `pkg/gateway/routes.go:11-96`.
const edgeRuleNegativeTTL = 30 * time.Second

type EdgeRuleCache struct {
	generation uint64
	now        func() time.Time
	// mu (ADR-091 hardening PR-A) is a sync.RWMutex rather than the
	// previous sync.Mutex. The hot path is read-dominant (every
	// cache hit + every cache miss walks the map under at least an
	// RLock), and Reset / Put are infrequent (Reset fires only on
	// db.NotifyEdgeRuleChanged; Put fires only on a loader's
	// compile-and-fill). Widening to RWMutex lets concurrent hits
	// proceed without serialising on a write lock; misses that
	// trigger a Put wait until the loader releases the write lock,
	// which is the established semantic for the structural twin
	// pkg/gateway/public_auth_cache.go:46-162. MoveToFront under
	// write-lock is a container/list O(1) op that holds the
	// critical section for nanoseconds; the value-copy before
	// re-acquiring the write lock keeps the RLock phase short.
	mu   sync.RWMutex
	cap  int
	ll   *list.List               // front = most recently used
	byID map[string]*list.Element // host → element (element.Value is *HostEntry)
}

// HostEntry is the per-host compiled-rule set the EdgeRuleCache
// LRU stores. cmd-side constructs one per cache miss via
// cmd/gatewayd-internal/edge_rules.go::loadHost, which runs every
// compile* over the same []state.EdgeRule slice and stitches the
// results into a single HostEntry. PathGlobErrs aggregates the
// path-glob parse errors from all compileXxx calls so the
// loader can re-emit them at WARN on subsequent reads without
// re-running path.Match. (PR 5 widened the slice count from 4 to
// 7; the audit tuple is reused verbatim — malformed CIDRs flow
// through the same PathGlobErrs slice per compileIPRules.)
//
// Exported so the cmd-side loader can populate the cache via
// `cache.Put(host, &gateway.HostEntry{...})` — see how the cmd-side
// loadHost builds the entry. PR 5 widens with CORS / JWT / IP slots.
type HostEntry struct {
	expiresAt time.Time
	Host      string
	Route     []EdgeRuleResolved
	Rewrite   []EdgeRuleRewriteResolved
	Redirect  []EdgeRuleRedirectResolved
	Headers   []EdgeRuleHeadersResolved
	CORS      []EdgeRuleCORSResolved
	JWT       []EdgeRuleJWTResolved
	IP        []EdgeRuleIPResolved
	Validate  []EdgeRuleValidateResolved
	// Limit carries the kind=limit subset (ADR-091 D24). Same
	// shape as Validate above; the applier
	// (handler.go::applyEdgeRuleLimit) installs MaxBytesReader on
	// r.Body at the per-rule cap. Stored as a plain slice to match
	// the surrounding fields — the cache primitive is
	// kind-agnostic and the cmd-side loader threads one slice per
	// kind into the HostEntry.
	Limit []EdgeRuleLimitResolved
	// Maintenance carries the kind=maintenance subset (ADR-091
	// amendment). Same shape as Limit / Validate above; the applier
	// (handler.go::applyEdgeRuleMaintenance, §4.1.2.13) short-circuits
	// a matched (host, path, method) request with 503 + Retry-After
	// BEFORE auth, BEFORE wake. Stored as a plain slice to match the
	// surrounding fields — the cache primitive is kind-agnostic and
	// the cmd-side loader threads one slice per kind into the
	// HostEntry. The struct itself lives in edge_rules_maintenance.go
	// (file-level split mirroring edge_rules_limit.go).
	Maintenance []EdgeRuleMaintenanceResolved
	// Geo carries the kind=geo subset (ADR-091 D21). Same shape
	// as IP / Limit above; the applier
	// (handler.go::applyEdgeRuleGeo) reads the trusted XFF entry,
	// consults the pkg/geoip.Reader for the country code, and
	// evaluates against this rule's Allow/Deny sets. Stored as a
	// plain slice to match the surrounding fields — the cache
	// primitive is kind-agnostic and the cmd-side loader threads
	// one slice per kind into the HostEntry.
	Geo []EdgeRuleGeoResolved
	// Throttle carries the kind=throttle subset (ADR-091 D20.5
	// amendment, issue #881). Same shape as Limit / Maintenance
	// above; the applier (handler.go::applyEdgeRuleThrottle) reads
	// the resolved rule's RequestsPerSecond + Burst, constructs a
	// rule-scoped bucket key (AppID + "\x00" + ID), and decrements
	// it on every passing request. The bucket is held in the shared
	// limiter built with NewLimiterWithLRU (#887) so the wake path
	// never exceeds the configured cardinality. Stored as a plain
	// slice to match the surrounding fields — the cache primitive
	// is kind-agnostic and the cmd-side loader threads one slice
	// per kind into the HostEntry.
	Throttle []EdgeRuleThrottleResolved
	// Budget carries the kind=budget subset (ADR-093). The applier
	// (handler.go::applyEdgeRuleBudget) stamps the per-rule
	// BudgetMs onto r.Context() via reqbudget.WithRemaining. Stored
	// as a plain slice to match the surrounding fields; the cache
	// primitive is kind-agnostic and the cmd-side loader threads
	// one slice per kind into the HostEntry.
	Budget []EdgeRuleBudgetResolved
	// Cache carries the kind=cache subset (ADR-122). The applier
	// (handler.go::applyEdgeRuleCache) consults the matched rule's
	// MaxAgeSeconds + StaleIfErrorSeconds + VaryOn, computes a
	// CacheKey from (AppID, DeploymentID, RuleID, Method,
	// NormalizedPath, VaryHash), and consults the runtime
	// ResponseCache (pkg/gateway/response_cache.go). A fresh hit
	// serves the cached body BEFORE the wake gate (no VM, no
	// gb_ram_hour). Stored as a plain slice to match the
	// surrounding fields; the cache primitive is kind-agnostic and
	// the cmd-side loader threads one slice per kind into the
	// HostEntry.
	Cache        []EdgeRuleCacheResolved
	PathGlobErrs []PathGlobError
}

// NewEdgeRuleCache returns a cache holding up to `capacity` host
// entries. A capacity < 1 is clamped to 1 (matches RouteCache).
func NewEdgeRuleCache(capacity int) *EdgeRuleCache {
	if capacity < 1 {
		capacity = 1
	}
	return &EdgeRuleCache{now: time.Now, cap: capacity, ll: list.New(), byID: map[string]*list.Element{}}
}

// Get returns the cached `kind=route` slice for host and whether
// the entry was present, promoting the entry on hit. Returns
// (nil, false) when the host has no current entry. A cached host
// with no route rules returns (nil, true), like every other kind.
//
// Returns a value-copy of the underlying slice so callers can
// mutate without poisoning the cache (mirrors the RouteCache
// value-copy pattern at `pkg/gateway/routes.go`).
func (c *EdgeRuleCache) Get(host string) ([]EdgeRuleResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Route == nil {
		return nil, true
	}
	src := entry.Route
	out := make([]EdgeRuleResolved, len(src))
	copy(out, src)
	return out, true
}

// GetRewrite / GetRedirect / GetHeaders are the PR 4 per-kind
// accessors mirroring Get. Each returns a value-copy of the
// underlying slice and a hit bool; nil slice with ok=true means
// "entry exists but no rule of this kind for this host".
func (c *EdgeRuleCache) GetRewrite(host string) ([]EdgeRuleRewriteResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Rewrite == nil {
		return nil, true
	}
	src := entry.Rewrite
	out := make([]EdgeRuleRewriteResolved, len(src))
	copy(out, src)
	return out, true
}

func (c *EdgeRuleCache) GetRedirect(host string) ([]EdgeRuleRedirectResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Redirect == nil {
		return nil, true
	}
	src := entry.Redirect
	out := make([]EdgeRuleRedirectResolved, len(src))
	copy(out, src)
	return out, true
}

func (c *EdgeRuleCache) GetHeaders(host string) ([]EdgeRuleHeadersResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Headers == nil {
		return nil, true
	}
	src := entry.Headers
	out := make([]EdgeRuleHeadersResolved, len(src))
	copy(out, src)
	return out, true
}

// GetCORS / GetJWT / GetIP are the PR 5 per-kind accessors mirroring
// GetRewrite / GetRedirect / GetHeaders. Same shape: a value-copy of
// the underlying slice and a hit bool; nil slice with ok=true means
// "entry exists but no rule of this kind for this host".
func (c *EdgeRuleCache) GetCORS(host string) ([]EdgeRuleCORSResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.CORS == nil {
		return nil, true
	}
	src := entry.CORS
	out := make([]EdgeRuleCORSResolved, len(src))
	copy(out, src)
	return out, true
}

func (c *EdgeRuleCache) GetJWT(host string) ([]EdgeRuleJWTResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.JWT == nil {
		return nil, true
	}
	src := entry.JWT
	out := make([]EdgeRuleJWTResolved, len(src))
	copy(out, src)
	return out, true
}

func (c *EdgeRuleCache) GetIP(host string) ([]EdgeRuleIPResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.IP == nil {
		return nil, true
	}
	src := entry.IP
	out := make([]EdgeRuleIPResolved, len(src))
	copy(out, src)
	return out, true
}

// GetValidate is the PR-B accessor for the kind=validate slice.
// Same shape as GetIP: returns a value-copy of the underlying slice
// and a hit bool; nil slice with ok=true means "entry exists but
// no validate rule for this host".
func (c *EdgeRuleCache) GetValidate(host string) ([]EdgeRuleValidateResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Validate == nil {
		return nil, true
	}
	src := entry.Validate
	out := make([]EdgeRuleValidateResolved, len(src))
	copy(out, src)
	return out, true
}

// GetLimit is the D24 accessor for the kind=limit slice. Same shape
// as GetValidate: returns a value-copy of the underlying slice and
// a hit bool; nil slice with ok=true means "entry exists but no
// limit rule for this host".
func (c *EdgeRuleCache) GetLimit(host string) ([]EdgeRuleLimitResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Limit == nil {
		return nil, true
	}
	src := entry.Limit
	out := make([]EdgeRuleLimitResolved, len(src))
	copy(out, src)
	return out, true
}

// GetMaintenance is the kind=maintenance accessor. Same shape as
// GetLimit: returns a value-copy of the underlying slice and a hit
// bool; nil slice with ok=true means "entry exists but no
// maintenance rule for this host". Coarse-gate apps.maintenance_mode
// fires before this method is called (handler.go::applyAppsMaintenanceMode),
// so a hit on the coarse gate short-circuits without consulting
// this slice.
func (c *EdgeRuleCache) GetMaintenance(host string) ([]EdgeRuleMaintenanceResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Maintenance == nil {
		return nil, true
	}
	src := entry.Maintenance
	out := make([]EdgeRuleMaintenanceResolved, len(src))
	copy(out, src)
	return out, true
}

// GetGeo mirrors GetIP for the kind=geo subset (ADR-091 D21).
// The Geo slice is a value-copy of the underlying cache entry,
// AND each per-entry map (Methods/Allow/Deny) is deep-copied —
// the unique inner-map-mutation test (edge_rules_geo_test.go
// TestCache_GetGeo_DeepCopiesInnerMaps) pins the contract that
// a caller mutating the returned Allow/Deny/Methods for any
// reason (e.g. a "consume" pattern that removes a matched
// country to short-circuit subsequent walks) MUST NOT poison
// the cache. The other Get* family members only copy the
// outer slice — their maps are not mutated by callers — so
// this is the only accessor that pays the inner-map copy cost.
func (c *EdgeRuleCache) GetGeo(host string) ([]EdgeRuleGeoResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Geo == nil {
		return nil, true
	}
	src := entry.Geo
	out := make([]EdgeRuleGeoResolved, len(src))
	for i, r := range src {
		out[i] = r
		if r.Methods != nil {
			m := make(map[string]bool, len(r.Methods))
			for k, v := range r.Methods {
				m[k] = v
			}
			out[i].Methods = m
		}
		if r.Allow != nil {
			a := make(map[string]struct{}, len(r.Allow))
			for k := range r.Allow {
				a[k] = struct{}{}
			}
			out[i].Allow = a
		}
		if r.Deny != nil {
			d := make(map[string]struct{}, len(r.Deny))
			for k := range r.Deny {
				d[k] = struct{}{}
			}
			out[i].Deny = d
		}
	}
	return out, true
}

// GetThrottle is the kind=throttle accessor (ADR-091 D20.5
// amendment, issue #881). Same shape as GetLimit: returns a
// value-copy of the underlying slice and a hit bool; nil slice
// with ok=true means "entry exists but no throttle rule for this
// host". The applier (handler.go::applyEdgeRuleThrottle) consults
// the priority-ordered slice via PickFirstThrottleMatch.
func (c *EdgeRuleCache) GetThrottle(host string) ([]EdgeRuleThrottleResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Throttle == nil {
		return nil, true
	}
	src := entry.Throttle
	out := make([]EdgeRuleThrottleResolved, len(src))
	copy(out, src)
	return out, true
}

// GetBudget is the kind=budget accessor (ADR-093). Same shape as
// GetLimit: returns a value-copy of the underlying slice and a hit
// bool; nil slice with ok=true means "entry exists but no budget
// rule for this host" — applyEdgeRuleBudget falls back to the
// plan-level default budget on nil.
func (c *EdgeRuleCache) GetBudget(host string) ([]EdgeRuleBudgetResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Budget == nil {
		return nil, true
	}
	src := entry.Budget
	out := make([]EdgeRuleBudgetResolved, len(src))
	copy(out, src)
	return out, true
}

// GetCache returns the per-host cache-rule slice (ADR-122).
// nil, false = no entry for host (cmd-side loader needs to
// populate it via PutEntry); nil, true = entry exists but has
// no cache rules (applyEdgeRuleCache falls back to "no cache
// rule" → cache miss); non-nil, true = priority-ordered
// slice for applyEdgeRuleCache to consult.
//
// Same defensive-copy discipline as GetBudget above: the
// returned slice is a fresh backing array so a racing Put
// (db.NotifyEdgeRuleChanged) does not mutate the slice the
// applier is iterating.
func (c *EdgeRuleCache) GetCache(host string) ([]EdgeRuleCacheResolved, bool) {
	entry, ok := c.getEntry(host)
	if !ok {
		return nil, false
	}
	if entry.Cache == nil {
		return nil, true
	}
	src := entry.Cache
	out := make([]EdgeRuleCacheResolved, len(src))
	copy(out, src)
	return out, true
}

// getEntry promotes the entry on hit and returns it. Internal —
// the Get* family wraps this so each returns a typed slice.
//
// Lock discipline (ADR-091 hardening PR-A): RLock for the entry
// fetch + value-copy, Lock only for the MoveToFront (a
// container/list O(1) mutation that must be exclusive). The pattern
// mirrors pkg/gateway/public_auth_cache.go:130-147 (fast-path
// RLock + slow-path Lock-on-evict with double-checked re-acquire).
// A racing Put between the RUnlock and the Lock may have replaced
// the entry (or evicted it); the post-re-acquire `el, ok := …`
// re-check handles that case — if the host is gone, the caller
// sees a clean miss and the loader re-hits PG on the next Get.
func (c *EdgeRuleCache) getEntry(host string) (*HostEntry, bool) {
	c.mu.RLock()
	_, ok := c.byID[host]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check under the write lock: a concurrent Reset / Put may
	// have evicted or replaced the entry between RUnlock and Lock.
	// container/list addresses are stable across Reset (we re-init
	// ll but the old elements are unreachable), so it's enough to
	// look the element up by the host key again and discard the
	// pre-Lock value. The discard is intentional — staticcheck
	// (SA4006) flags the pre-Lock `el` as unused otherwise.
	el, ok := c.byID[host]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*HostEntry)
	if !entry.expiresAt.IsZero() && !c.now().Before(entry.expiresAt) {
		c.removeElement(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return entry, true
}

// Put caches a successfully loaded rule set. Nil means no usable result;
// an empty set is a negative entry with a bounded lifetime (ADR-160).
func (c *EdgeRuleCache) Put(host string, entry *HostEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(host, entry)
}

// Generation fences a database read against invalidation while it is in flight.
func (c *EdgeRuleCache) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

// PutIfGeneration does not repopulate the cache with a read started before Reset.
func (c *EdgeRuleCache) PutIfGeneration(host string, entry *HostEntry, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation == generation {
		c.putLocked(host, entry)
	}
}

func (c *EdgeRuleCache) putLocked(host string, entry *HostEntry) {
	if entry == nil {
		return
	}
	// Cache metadata belongs to this insertion, not to the loader's result.
	cached := *entry
	cached.Host = host
	cached.expiresAt = time.Time{}
	if !cached.hasRules() {
		cached.expiresAt = c.now().Add(edgeRuleNegativeTTL)
	}
	if el, ok := c.byID[host]; ok {
		el.Value = &cached
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&cached)
	c.byID[host] = el
	if c.ll.Len() > c.cap {
		c.evictLRU()
	}
}

func (e *HostEntry) hasRules() bool {
	return len(e.Route)+len(e.Rewrite)+len(e.Redirect)+len(e.Headers)+
		len(e.CORS)+len(e.JWT)+len(e.IP)+len(e.Validate)+len(e.Limit)+
		len(e.Maintenance)+len(e.Geo)+len(e.Throttle)+len(e.Budget)+len(e.Cache) > 0
}

// Reset drops every cached entry. gatewayd-internal calls this on
// `db.NotifyEdgeRuleChanged` (mirrors `PGBackend.FlushRoutes`).
func (c *EdgeRuleCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.ll.Init()
	c.byID = map[string]*list.Element{}
}

// Len returns the number of cached host entries.
func (c *EdgeRuleCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

func (c *EdgeRuleCache) evictLRU() {
	if el := c.ll.Back(); el != nil {
		c.removeElement(el)
	}
}

func (c *EdgeRuleCache) removeElement(el *list.Element) {
	c.ll.Remove(el)
	delete(c.byID, el.Value.(*HostEntry).Host)
}

// EdgeRuleMatcher is the narrow contract the gateway handler uses
// to consult per-request edge rules. Implementations MUST be safe
// for concurrent use. PR 5 widens with MatchCORS / MatchJWT / MatchIP
// on top of the same shape. Future kind matchers embed a
// `noOpEdgeRuleMatcher` to inherit the no-op default.
//
// MatchRoute returns the highest-priority `kind=route` rule whose
// host, path, and method match the inbound request, or nil if no
// rule applies. The caller (handler.go) substitutes the resolved
// target App for the inbound-host's App before downstream gates run.
//
// MatchRewrite / MatchRedirect / MatchHeaders are the PR 4 per-kind
// matchers. Same priority-ASC + path/methods filter shape as
// MatchRoute; each returns the highest-priority rule of its kind
// that matches the inbound (host, path, method), or nil on miss.
//
// MatchCORS / MatchJWT / MatchIP are the PR 5 security-gate
// matchers. Same priority-ASC + path/methods filter shape; the
// appliers (handler.go) decide whether to short-circuit, stamp
// headers, or write 401/403 on a hit.
//
// MatchValidate is the PR-B matcher for the kind=validate subset.
// The applier buffers r.Body and consults pkg/edgevalidate.Validator
// via the cmd-side adapter; the matcher itself only resolves the
// highest-priority matching rule (no body read here — that lives
// in handler.go to keep pkg/gateway free of io.Reader juggling).
//
// Reset drops every cached entry. Called by the gatewayd notify
// loop on `db.NotifyEdgeRuleChanged`.
type EdgeRuleMatcher interface {
	MatchRoute(ctx context.Context, host, path, method string) *EdgeRuleResolved
	MatchRewrite(ctx context.Context, host, path, method string) *EdgeRuleRewriteResolved
	MatchRedirect(ctx context.Context, host, path, method string) *EdgeRuleRedirectResolved
	MatchHeaders(ctx context.Context, host, path, method string) *EdgeRuleHeadersResolved
	MatchCORS(ctx context.Context, host, path, method string) *EdgeRuleCORSResolved
	MatchJWT(ctx context.Context, host, path, method string) *EdgeRuleJWTResolved
	MatchIP(ctx context.Context, host, path, method string) *EdgeRuleIPResolved
	MatchValidate(ctx context.Context, host, path, method string) *EdgeRuleValidateResolved
	MatchLimit(ctx context.Context, host, path, method string) *EdgeRuleLimitResolved
	MatchMaintenance(ctx context.Context, host, path, method string) *EdgeRuleMaintenanceResolved
	MatchGeo(ctx context.Context, host, path, method string) *EdgeRuleGeoResolved
	MatchThrottle(ctx context.Context, host, path, method string) *EdgeRuleThrottleResolved
	// MatchBudget is the ADR-093 matcher for the kind=budget
	// subset. The applier stamps the per-rule BudgetMs onto the
	// inbound ctx via reqbudget.WithRemaining; the matcher itself
	// only resolves the highest-priority matching rule (no budget
	// math here — that lives in handler.go's applyEdgeRuleBudget
	// + reqbudget.WithRemaining so the per-hop remaining-time
	// propagation logic stays in one place).
	MatchBudget(ctx context.Context, host, path, method string) *EdgeRuleBudgetResolved
	// MatchCache is the ADR-122 matcher for the kind=cache
	// subset. The applier (handler.go::applyEdgeRuleCache) reads
	// the matched rule's MaxAgeSeconds / StaleIfErrorSeconds /
	// VaryOn and consults the in-process ResponseCache; the
	// matcher itself only resolves the highest-priority matching
	// rule (the cache key + storage live in handler.go so the
	// wake-gate interaction stays in one place).
	MatchCache(ctx context.Context, host, path, method string) *EdgeRuleCacheResolved
	Reset()
}

// EdgeRuleAuditor is the narrow emit-only slice the matcher uses
// to record rule firings. Declared locally so pkg/gateway doesn't
// import cmd/* (avoid a reverse dep) and so tests can inject a
// counting fake. Best-effort semantics — the matcher never blocks
// a request on a failed emit (mirrors RequireAuthnAuditor at
// `pkg/gateway/handler.go:194-204`).
type EdgeRuleAuditor interface {
	Emit(ctx context.Context, kind string, subject *string, data map[string]any)
}

// JWTVerifier (ADR-091 / issue #561 PR 5) is the narrow surface
// pkg/gateway uses for kind=jwt verification. The cmd-side wires the
// pkg/edgejwks.Verifier via a thin adapter that conforms to this
// interface; pkg/gateway itself never imports pkg/edgejwks, so
// the dep direction stays one-way (gateway is a leaf, like
// pkg/auth.Middleware being consumed by the daemons but not
// importing cmd-side state types).
//
// rawToken is the stripped bearer suffix (no "Bearer " prefix) —
// handler.go does strings.TrimPrefix before calling. The verifier
// looks up the rule's JWKSURL in its per-URL cache, fetches the
// keyset if needed, picks the right key by kid, verifies the
// signature + exp + iss + aud + required_claims, and returns the
// parsed claims. Errors are package sentinels (edgejwks.ErrJWT*)
// so handler.go can map them to distinct audit + metric outcomes.
type JWTVerifier interface {
	Verify(ctx context.Context, rawToken string, rule *EdgeRuleJWTResolved) (claims *JWTClaims, err error)
}

// JWTClaims is the parsed subset pkg/gateway cares about. Mirrors
// pkg/edgejwks.Claims (same field set; pkg/gateway doesn't import
// pkg/edgejwks so the struct is duplicated here — drift would
// surface as a mismatch when the cmd-side adapter copies the
// fields over, which is intentional).
//
// Custom is the string→string subset of additional claims the rule
// required (pkg/edgejwks.Claims.Custom). Phase 3 (ADR-104, issue
// #881 Phase 3) threads this through to applyEdgeRuleThrottle so a
// rule with key_by="jwt_claim" can look up the named claim value
// for bucket-key construction. Pre-Phase-3 code never read Custom
// — adding the field is a non-breaking widening; zero-value (nil)
// is the prior behaviour.
type JWTClaims struct {
	Subject string
	Issuer  string
	Aud     []string
	Exp     time.Time
	Custom  map[string]string
}

// EdgeValidateIn is the per-call input the Validator consults. The
// applier (handler.go) buffers r.Body into Body (preserving the
// body for the proxy leg via r.Body = io.NopCloser(bytes.NewReader))
// before calling. ContentType is r.Header.Get("Content-Type") at
// the time of the call.
//
// The shape mirrors pkg/edgevalidate.In exactly. pkg/gateway keeps
// its own copy because pkg/gateway has no dep on pkg/edgevalidate
// (mirrors the JWTClaims discipline above); drift would surface
// at the cmd-side adapter when it copies fields.
type EdgeValidateIn struct {
	Body        []byte
	ContentType string
}

// EdgeValidateFieldError is one per-field entry of a validation
// failure. Mirrors pkg/edgevalidate.FieldError exactly. The handler
// lifts it into api.FieldError on the 422 problem+json; pkg/api
// owns the wire-shape definition so customers see one shape across
// rules.
type EdgeValidateFieldError struct {
	Field    string
	Expected string
	Got      string
}

// Reason (issue #975 #3 / Mega-Foundation #979-a) maps the
// schema-side keyword the validator reported to the bounded
// taxonomy the gateway metric uses. The mapping is the same as
// pkg/edgevalidate.FieldError.Reason — duplicated here so the
// gateway-side wrapper doesn't need to import its validator
// backend. Keeping the two in lockstep is the test's
// responsibility (see pkg/gateway/edge_rules_test.go).
func (fe *EdgeValidateFieldError) Reason() string {
	if fe == nil {
		return reasonOther
	}
	switch fe.Expected {
	case "required":
		return "required_missing"
	case "type":
		return "type_mismatch"
	case "additionalProperties":
		return "additional_properties_not_allowed"
	case "enum":
		return "enum_violation"
	case "format":
		return "format_violation"
	}
	return reasonOther
}

// EdgeValidateResult is the per-call outcome of Validator.Validate.
// OK is true on match; FirstError is non-nil only on mismatch.
// SchemaDigest is always populated so the handler can tag
// audit/metric events without re-hashing.
type EdgeValidateResult struct {
	OK           bool
	SchemaDigest [32]byte
	FirstError   *EdgeValidateFieldError
}

// Validator (PR-B) is the narrow surface pkg/gateway uses for
// kind=validate validation. The cmd-side wires the
// pkg/edgevalidate.Manager via a thin adapter that conforms to this
// interface; pkg/gateway itself never imports pkg/edgevalidate, so
// the dep direction stays one-way (gateway is a leaf, like with
// pkg/edgejwks).
//
// The validator is consulted AFTER the body has been buffered
// (handler.go is responsible for the read). Errors are package
// sentinels (ErrSchemaInvalid / ErrSchemaExternalRef /
// ErrSchemaEmpty / ErrSchemaTooLarge); the handler maps them to
// distinct audit + metric outcomes.
//
// nil-safe: the Handler field is nil in dev mode and applyEdgeRuleValidate
// short-circuits when nil.
type Validator interface {
	Validate(ctx context.Context, req *EdgeValidateIn, rule *EdgeRuleValidateResolved) (*EdgeValidateResult, error)
}

// Edge-validate sentinels, declared in pkg/gateway so the handler
// can errors.Is them without importing pkg/edgevalidate. The
// cmd-side adapter (cmd/gatewayd-internal/edge_validate.go)
// wraps pkg/edgevalidate.Err* sentinels into these when
// forwarding errors up through Validator.Validate.
//
// Why duplicate rather than re-export: pkg/edgevalidate is the
// canonical source of truth for the underlying library; pkg/gateway
// owns the applier-side error vocabulary. Drift would surface as
// a missing sentinel when the adapter forgets to wrap, which is
// intentional (the applier would see the literal error message
// in the 500 audit row).
var (
	ErrValidateSchemaInvalid     = errors.New("edgevalidate: schema is invalid")
	ErrValidateSchemaEmpty       = errors.New("edgevalidate: schema is empty")
	ErrValidateSchemaTooLarge    = errors.New("edgevalidate: schema exceeds MaxSchemaBytes")
	ErrValidateSchemaExternalRef = errors.New("edgevalidate: schema contains an external $ref or $id")
)

// noOpEdgeRuleMatcher is the default Embedding target the matcher
// implementations use to inherit default no-op behavior for the
// kinds they don't ship. Today's production impl
// `cmd/gatewayd-internal.edgeRules` doesn't embed it (PR 5 ships
// all Match* methods); the type exists for the
// forward-compatible interface shape so a future kind's impl can
// embed it and only override the kinds it ships.
type noOpEdgeRuleMatcher struct{}

func (noOpEdgeRuleMatcher) MatchRoute(context.Context, string, string, string) *EdgeRuleResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchRewrite(context.Context, string, string, string) *EdgeRuleRewriteResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchRedirect(context.Context, string, string, string) *EdgeRuleRedirectResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchHeaders(context.Context, string, string, string) *EdgeRuleHeadersResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchCORS(context.Context, string, string, string) *EdgeRuleCORSResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchJWT(context.Context, string, string, string) *EdgeRuleJWTResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchIP(context.Context, string, string, string) *EdgeRuleIPResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchValidate(context.Context, string, string, string) *EdgeRuleValidateResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchLimit(context.Context, string, string, string) *EdgeRuleLimitResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchMaintenance(context.Context, string, string, string) *EdgeRuleMaintenanceResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchGeo(context.Context, string, string, string) *EdgeRuleGeoResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchThrottle(context.Context, string, string, string) *EdgeRuleThrottleResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchBudget(context.Context, string, string, string) *EdgeRuleBudgetResolved {
	return nil
}
func (noOpEdgeRuleMatcher) MatchCache(context.Context, string, string, string) *EdgeRuleCacheResolved {
	return nil
}
func (noOpEdgeRuleMatcher) Reset() {}

// ResolveTargetApp is the `AppBySlug` closure the matcher needs to
// swap the gateway.App when a `kind=route` rule fires. Production
// wraps `state.Store.AppBySlug` (`pkg/state/store.go:890`) at
// `cmd/gatewayd-internal/run.go`. The closure returns `(App{}, false)`
// when the slug is not found or the target is cross-account — the
// matcher emits `edge_rule.route_blocked` audit + `outcome=blocked`
// metric in that case (defense-in-depth on top of the apid create-
// time same-account guarantee at
// `cmd/apid/handlers_edge_rules.go:184-201`).
type ResolveTargetApp func(ctx context.Context, slug string) (App, bool)

// PickFirstRouteMatch is the pure-Go filter used by
// cmd/gatewayd-internal/edge_rules.go::gatewaydEdgeRules.MatchRoute
// after the cache returns the priority-ordered slice. Exported so
// the production loader (cmd-side) can call it without poking at
// the unexported helper, and pinned in pkg/gateway unit tests so
// the production filter can't silently drift. Walks the slice
// priority-ASC (lower number = earlier = first-match-wins) and
// returns the first rule whose path glob + methods filter match.
//
// methods filter:
//
//   - empty map = any method matches
//   - non-empty map = request method MUST be present (case-folded
//     to upper by the loader; HTTP method names are
//     case-sensitive per RFC 7231 §4.1 but the gateway stores
//     them upper-cased via state.EdgeRuleResponse.MatchMethods)
//   - the request's own method is matched as-given (the handler
//     passes r.Method which Go returns upper-case)
//
// path glob: passed through stdlib path.Match; "" = match all;
// "*" = match all; "/api/*" = prefix-wildcard on the second
// segment.
func PickFirstRouteMatch(rules []EdgeRuleResolved, path, method string) *EdgeRuleResolved {
	return pickFirstMatch(rules, path, method)
}

// PickFirstRewriteMatch / PickFirstRedirectMatch / PickFirstHeadersMatch
// are the PR 4 per-kind mirrors of PickFirstRouteMatch. Same shape:
// priority-ASC walk, methods filter (O(1) via map lookup), path glob
// via path.Match. Each is called from its own gatewaydEdgeRules
// Match* method after the cache returns the priority-ordered slice.
// Generics are deliberately avoided here — three small private copies
// keep the filter logic in pkg/gateway unit tests without exposing a
// generic helper the wider codebase doesn't need.
//
// Exported so the cmd-side loader (cmd/gatewayd-internal/edge_rules.go)
// can call them without poking at unexported helpers.
func PickFirstRewriteMatch(rules []EdgeRuleRewriteResolved, path, method string) *EdgeRuleRewriteResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

func PickFirstRedirectMatch(rules []EdgeRuleRedirectResolved, path, method string) *EdgeRuleRedirectResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

func PickFirstHeadersMatch(rules []EdgeRuleHeadersResolved, path, method string) *EdgeRuleHeadersResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

// PickFirstCORSMatch / PickFirstJWTMatch / PickFirstIPMatch are the
// PR 5 per-kind mirrors of PickFirstRouteMatch. Same shape:
// priority-ASC walk, methods filter (O(1) via map lookup), path glob
// via path.Match. Each is called from its own gatewaydEdgeRules
// Match* method after the cache returns the priority-ordered slice.
// Three small copies keep the per-kind return types precise without
// paying for a runtime-type assertion on every request.
func PickFirstCORSMatch(rules []EdgeRuleCORSResolved, path, method string) *EdgeRuleCORSResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

func PickFirstJWTMatch(rules []EdgeRuleJWTResolved, path, method string) *EdgeRuleJWTResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

func PickFirstIPMatch(rules []EdgeRuleIPResolved, path, method string) *EdgeRuleIPResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

// PickFirstValidateMatch is the PR-B mirror of PickFirstIPMatch.
// Same priority-ASC + methods + path-glob filter shape; returns
// the highest-priority matching validate rule, or nil on miss.
// The body read + schema lookup happens in handler.go.
func PickFirstValidateMatch(rules []EdgeRuleValidateResolved, path, method string) *EdgeRuleValidateResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

// PickFirstGeoMatch is the §4.1.2.8b matcher for kind=geo
// (ADR-091 D21). Mirrors PickFirstIPMatch's priority-ASC +
// path/methods filter; the actual country-code lookup against
// the inbound client IP happens in applyEdgeRuleGeo (handler.go)
// AFTER path/methods match — the lookup is the gate's expensive
// step, so we short-circuit on a non-matching path-glob before
// paying it.
func PickFirstGeoMatch(rules []EdgeRuleGeoResolved, path, method string) *EdgeRuleGeoResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

// pickFirstMatch is the route-specific helper. Generic over the
// resolved type via the small interface below so we avoid three
// near-identical loops in the common-case MatchRoute path.
//
// All four pick* helpers (pickFirstMatch for route +
// pickFirstRewriteMatch / pickFirstRedirectMatch / pickFirstHeadersMatch)
// share the same filter semantics — methods map, path glob, first hit.
// Splitting them keeps the per-kind return types precise without
// paying for a runtime-type assertion on every request.
func pickFirstMatch(rules []EdgeRuleResolved, path, method string) *EdgeRuleResolved {
	for i := range rules {
		r := &rules[i]
		if r.Methods != nil && !r.Methods[method] {
			continue
		}
		if r.PathGlob != "" {
			ok, _ := pathGlobMatch(r.PathGlob, path)
			if !ok {
				continue
			}
		}
		return r
	}
	return nil
}

// pathGlobMatch is a tiny adapter over stdlib path.Match that
// honours the two sentinel patterns the gateway rules allow:
// "" (any path) and "*" (any path). Stdlib path.Match treats
// both as errors for the empty / star input, so we short-circuit.
func pathGlobMatch(glob, p string) (bool, error) {
	if glob == "" || glob == "*" {
		return true, nil
	}
	return pathMatch(glob, p)
}
