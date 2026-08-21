/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only per-app public-URL auth shape on AppResponse (issue #477 / ADR-077 + ADR-118). Mirrors the row contents without the plaintext credentials. The redaction posture is a load-bearing invariant — see ADR-077 §Decision 're-redaction invariant': neither basic_user nor basic_pass is EVER returned on the wire, even when mode='basic'. To rotate credentials, the customer PATCHes a fresh public_auth block.
 */
export type PublicAuthStatus = {
  /**
   * Active auth mode. One of 'open', 'bearer', 'basic', 'ip_allowlist', 'internal_only', 'members_only'. Matches apps.public_auth_mode on disk; a PATCH 'open' cleared any prior sealed blob so a stale secretbox row never reaches a fresh request. 'internal_only' (ADR-119) requires an Authorization: Bearer JWT with aud='gregale.internal' signed by a Gregale daemon's Ed25519 key. 'members_only' (ADR-120) requires a valid faas_sid session cookie whose principal is an active member of apps.org_id — see PublicAuthBlock.mode for the write-side description.
   */
  mode: 'open' | 'bearer' | 'basic' | 'ip_allowlist' | 'internal_only' | 'members_only';
  /**
   * True iff the row carries a non-null apps.public_auth_basic blob (i.e. mode='basic' with credentials). A mode='basic' row without creds would 401 every request — has_basic_creds is the operator-greppable signal that the seal succeeded.
   */
  has_basic_creds: boolean;
  /**
   * ADR-118: integer count of CIDRs in apps.public_auth_ip_allowlist. Returned (not the CIDR strings themselves) so the dashboard can show 'app X has 3 CIDRs configured' without leaking the partner-customer ranges. Always 0 when mode != 'ip_allowlist'.
   */
  ip_allowlist_entry_count?: number;
};

