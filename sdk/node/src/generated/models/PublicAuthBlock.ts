/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app public-URL auth write shape (issue #477 / ADR-077 + ADR-118). Sent on PATCH /v1/apps/{slug}; apid seals the basic_user + basic_pass into a single APP_BASIC_AUTH secretbox blob before persistence. The plaintext is never echoed on read (see PublicAuthStatus). For mode='ip_allowlist' (ADR-118), ip_allowlist carries the per-app CIDR allowlist (Pro 16 max, Scale 64 max — Free/Hobby → 403 plan_public_auth_ip_allowlist_not_allowed).
 */
export type PublicAuthBlock = {
  /**
   * Auth mode (closed set). 'open' is the pre-#477 default (every request passes). 'bearer' requires Authorization: Bearer (Hobby+; 402 on Free). 'basic' requires HTTP Basic auth with sealed credentials (Pro+; 402 on Free/Hobby). 'ip_allowlist' (ADR-118) restricts the app to requests originating from a client IP inside the per-app CIDR allowlist (Pro+; 402 on Free/Hobby). 'internal_only' (ADR-119) restricts the app to requests carrying an Authorization: Bearer JWT with aud='gregale.internal' signed by a Gregale daemon's Ed25519 key (per-service public-key allowlist is operator-side; available on all plans). 'members_only' (ADR-120) restricts the app to requests carrying a valid faas_sid session cookie whose principal is an active member of apps.org_id (Hobby+; 402 on Free). Unknown values → 422 invalid_public_auth_mode.
   */
  mode: 'open' | 'bearer' | 'basic' | 'ip_allowlist' | 'internal_only' | 'members_only';
  /**
   * Basic-auth username (RFC 7617 §2). Plaintext at PATCH time; sealed into apps.public_auth_basic under the APP_BASIC_AUTH secretbox namespace. Required when mode='basic'; ignored otherwise. Range [1, 128] bytes after TrimSpace.
   */
  basic_user?: string;
  /**
   * Basic-auth password (RFC 7617 §2). Plaintext at PATCH time; sealed alongside basic_user under the APP_BASIC_AUTH secretbox namespace. Required when mode='basic'; ignored otherwise. Range [1, 256] bytes.
   */
  basic_pass?: string;
  /**
   * ADR-118: per-app ingress CIDR allowlist. Required when mode='ip_allowlist' (must be non-empty). Each entry is an RFC 4632 CIDR (e.g. '10.0.0.0/8' or '2001:db8::/32'); masklen /0 is rejected at the wire and by the apps_public_auth_ip_allowlist_cidr trigger. v4-mapped-v6 prefixes are rejected at the handler. After canonicalisation, the cap is plan.PublicAuthIPAllowlistMaxEntries (Pro 16, Scale 64). On the audit row, only the entry count is recorded — never the CIDR strings.
   */
  ip_allowlist?: Array<string>;
};

