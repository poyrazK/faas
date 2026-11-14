/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Result of `POST /v1/auth/verify-email/resend`.
 */
export type EmailVerificationResendResponse = {
  status: 'sent' | 'already_verified';
  email?: string;
};

