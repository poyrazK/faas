/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for consuming the emailed token after the cooldown.
 */
export type MFADisableEmailConfirmRequest = {
  /**
   * Opaque base64url token from the confirmation email.
   */
  token: string;
  csrf_token?: string;
};

