/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * New password for the authenticated account. Lets OAuth-only customers opt into password login, and customers who already have a password replace it.
 */
export type SetPasswordRequest = {
  password: string;
  /**
   * Double-submit token from `GET /v1/auth/csrf?action=set_password`;
   * must equal the `faas_csrf` cookie that call set.
   *
   */
  csrf_token: string;
  /**
   * Required when the account already has a password and the
   * session carries no step-up from the last 5 minutes
   * (ADR-140). Ignored for OAuth-only accounts.
   *
   */
  current_password?: string;
};

