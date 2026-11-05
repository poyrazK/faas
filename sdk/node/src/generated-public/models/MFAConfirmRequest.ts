/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for /confirm — a TOTP code and the existing password, if set.
 */
export type MFAConfirmRequest = {
  /**
   * 6-digit rotating code from the authenticator app.
   */
  totp: string;
  /**
   * Required when the account already has a password; authorizes the new factor.
   */
  current_password?: string;
  /**
   * Dashboard CSRF token. The dashboard sets a
   * `faas_csrf` cookie on every authenticated response; the
   * same value rides in this JSON-body field so the
   * server can compare them.
   * The token is opaque to the wire (sealed session
   * reference); size matches the cookie value.
   *
   */
  csrf_token?: string;
};

