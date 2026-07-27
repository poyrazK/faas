/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for /confirm — a single 6-digit TOTP code.
 */
export type MFAConfirmRequest = {
  /**
   * 6-digit rotating code from the authenticator app.
   */
  totp: string;
  /**
   * Dashboard CSRF token. The dashboard sets a
   * `faas_csrf` cookie on every authenticated response; the
   * same value rides in this JSON-body field so
   * `pkg/middleware.VerifyAuthenticated` can compare them.
   * The token is opaque to the wire (sealed session
   * reference); size matches the cookie value.
   *
   */
  csrf_token?: string;
};

