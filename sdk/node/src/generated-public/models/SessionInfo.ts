/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row per dashboard login (ADR-039 / IAM-3). The
 * cookie envelope carries the row's `id` as `sid`;
 * every authenticated request re-validates the row.
 * Revoked rows are filtered out of the
 * `GET /v1/auth/sessions` response.
 *
 */
export type SessionInfo = {
  /**
   * Session id. Also stamped as `sid` in the cookie envelope.
   */
  id: string;
  account_id: string;
  /**
   * Client IP captured at login (host portion only; IPv4 or IPv6). Empty when the request's RemoteAddr was unparseable.
   */
  issued_ip?: string;
  /**
   * User-Agent header captured at login. May be empty when the client suppressed the header.
   */
  issued_ua?: string;
  /**
   * When the session was minted (RFC 3339, UTC).
   */
  issued_at: string;
  /**
   * Last time the cookie cross-validated this row. Updated with a 5-minute debounce; continues post-revoke (operational signal only, not authorization).
   */
  last_seen_at?: string;
  /**
   * True when this row's id matches the calling cookie's sid. Exactly one row per list response has this flag set.
   */
  current_session: boolean;
};

