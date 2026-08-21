/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PUT /v1/apps/{slug}/static-egress-ip body (ADR-119). IP is
 * the canonical customer-supplied IPv4 (dotted-quad string).
 * The handler validates the family=4 + non-RFC1918 +
 * non-link-local + non-multicast shape before the column
 * write. Set=false with empty IP means "clear" — the same
 * wire body covers the DELETE /keep-IP promotion path
 * without a third endpoint.
 *
 */
export type SetAppStaticEgressIPRequest = {
  /**
   * Customer-supplied IPv4 (dotted-quad). Required when
   * `set=true`. Empty string when `set=false`.
   *
   */
  ip: string;
  /**
   * `true` to pin the IP; `false` to clear the pin.
   *
   */
  set: boolean;
};

