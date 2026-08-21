// public_auth.go — per-app public-URL auth mode constants
// (issue #477 / ADR-079). Lives in pkg/api (not
// pkg/api/webhooks.go or pkg/api/alerts.go) because the
// DTO shape and the seal-boundary cap are distinct from
// both webhook secrets and alert-rule secrets.
//
// The constants here are intentionally tiny — pkg/api
// never holds any sealed-bytes shape, only the validation
// cap. The seal namespace + secretbox layer live in
// cmd/apid/handlers_ext.go (write side) and
// cmd/gatewayd-internal/public_auth_unsealer.go (read
// side). Keep this file dependency-free so it can be
// imported from any layer without a cycle.

package api

// AppPublicAuthBasicMaxBytes bounds the plaintext payload
// the apid seal step accepts on PATCH mode='basic'.
// 385 = 128 (basic_user) + 1 (\n) + 256 (basic_pass) —
// the on-blob layout from cmd/apid/handlers_ext.go
// builds the plaintext as "<user>\n<pass>". The
// per-field bounds the PublicAuthBlock.Validate method
// enforces. Generous for pasted values from secret
// managers, rejects megabyte uploads. Mirrors
// AppWebhookSecretMaxBytes' posture (256) and the alert
// cap (AlertRuleWebhookSecretMaxBytes = 256).
const AppPublicAuthBasicMaxBytes = 385

// AppPublicAuthBasicUserMaxBytes bounds the basic_user
// field on PATCH mode='basic'. 128 matches the
// application-secrets key length cap and the typical
// RFC 7617 username convention. Anything larger is almost
// certainly a paste error.
const AppPublicAuthBasicUserMaxBytes = 128

// AppPublicAuthBasicPassMaxBytes bounds the basic_pass
// field on PATCH mode='basic'. 256 matches
// AppWebhookSecretMaxBytes and rejects the megabyte
// payload a botched secret-manager export would
// otherwise trigger.
const AppPublicAuthBasicPassMaxBytes = 256

// Per-app public-auth mode enum (issue #477 / ADR-079).
// Canonical strings live here in pkg/api. The state
// package has a parallel set (state.AppPublicAuthMode*)
// that MUST stay byte-for-byte in sync — the pgstore
// SQL uses the state-layer values directly while the
// validator + plan gate + CLI use the api-layer values,
// and a drift surfaces as a runtime SQL CHECK
// constraint failure on a PATCH the API accepted.
// pkg/api/public_auth_test.go pins the two surfaces
// equal at compile time so a future contributor adding
// a fourth mode updates both halves in one change.
// The closed-set matches apps_public_auth_mode_chk in
// migrations/00153_apps_public_auth.sql; a future addition
// to the wire surface must add a row to that CHECK AND
// extend this constant block AND the state-layer mirror.
const (
	// AppPublicAuthModeOpen is the pre-#477 default. The
	// app's public hostname serves anonymous traffic;
	// no Authorization header is consulted. Always
	// allowed on any plan.
	AppPublicAuthModeOpen = "open"
	// AppPublicAuthModeBearer requires a valid `Authorization: Bearer <fp_live_...>`
	// key with apps:read scope on the app's owning
	// account. Plan-gated to Hobby+. The
	// cross-account / cross-app authz check lives in
	// pkg/gateway/handler.go::enforcePublicAuth.
	AppPublicAuthModeBearer = "bearer"
	// AppPublicAuthModeBasic requires HTTP Basic auth
	// (RFC 7617 §2) with credentials sealed at PATCH
	// time under secretbox namespace
	// "APP_BASIC_AUTH" (cmd/apid/handlers_ext.go).
	// Plan-gated to Pro+. The gatewayd-internal
	// unseals the blob at boot via
	// cmd/gatewayd-internal/public_auth_unsealer.go.
	AppPublicAuthModeBasic = "basic"
	// AppPublicAuthModeIPAllowlist restricts the app's
	// public hostname to client IPs inside the per-app
	// CIDR list apps.public_auth_ip_allowlist (ADR-118).
	// Anything else 403s at the request layer in
	// pkg/gateway/handler.go::applyIngressIPAllowlist
	// (runs before applyEdgeRuleIP, before wake).
	// Plan-gated to Pro+; Free/Hobby use edge rules
	// (kind='ip') for the abuse-floor posture.
	AppPublicAuthModeIPAllowlist = "ip_allowlist"
	// AppPublicAuthModeInternalOnly restricts the app's
	// public hostname to requests carrying an
	// `Authorization: Bearer <JWT>` token with
	// `aud='gregale.internal'` signed by a Gregale
	// daemon's Ed25519 key (ADR-119). Anything else 403s
	// at pkg/gateway/handler.go::applyIngressInternalSvc
	// (runs after applyIngressIPAllowlist, before
	// applyEdgeRuleIP) AND at the synth-server path
	// (pkg/gateway/synth.go::handleSynthesize, so cron
	// cannot bypass the gate). Available on all plans;
	// the trust boundary is operator-side, not
	// human-side, so no plan gate applies.
	AppPublicAuthModeInternalOnly = "internal_only"
	// AppPublicAuthModeMembersOnly restricts the app's
	// public hostname to requests carrying a valid
	// IAM-6 session cookie (`faas_sid`) whose principal
	// has an active membership in apps.org_id (ADR-120).
	// Anything else 401s at the authn layer (no cookie /
	// revoked cookie / stolen-cookie defense fires first
	// via pkg/auth/middleware.RequireSession) and 403s at
	// the authz layer (cookie valid but caller not a
	// member of the owning org) in
	// pkg/gateway/handler.go::applyIngressMembersOnly.
	// Runs after applyIngressInternalSvc, before
	// applyEdgeRuleIP. Synth-server mirrors the gate at
	// pkg/gateway/synth.go::handleSynthesize (so cron
	// cannot bypass — cron has no human session). Plan-gated
	// to Hobby+ (the org/membership infrastructure is
	// Hobby+ via the OrgMembersMax ladder; Free personal-org
	// has exactly 1 member, so members_only on Free would
	// collapse to bearer with the same account — Free is
	// rejected to keep the abuse-floor posture clean).
	AppPublicAuthModeMembersOnly = "members_only"
)

// AppPublicAuthIPAllowlistMaxEntries bounds the per-app
// ingress IP allowlist CIDR count accepted on PATCH.
// Mirrors EgressAllowlistMaxSize's posture (Pro: 16,
// Scale: 64) — Free/Hobby return 0 and the apid PATCH
// handler rejects with 403 plan_public_auth_ip_allowlist_not_allowed.
// apid's updateApp rejects with 400
// public_auth_ip_allowlist_too_long when the PATCH
// body has more entries than the per-plan cap.
const AppPublicAuthIPAllowlistMaxEntries = 64
