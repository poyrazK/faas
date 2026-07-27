/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Token + new password for the password-reset submission.
 * Token is the base64url-encoded 32-byte value from the
 * email link (NOT the SHA-256 hash the server stores).
 *
 */
export type PasswordResetConfirm = {
  token: string;
  new_password: string;
};

