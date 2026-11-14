/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for `POST /v1/account/email`.
 */
export type AccountEmailChangeRequest = {
  email: string;
  /**
   * Required when the account has a password and no two-factor authentication.
   */
  current_password?: string;
  csrf_token?: string;
};

