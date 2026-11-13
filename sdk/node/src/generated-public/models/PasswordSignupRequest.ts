/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Email + password for signup. Same shape as PasswordLoginRequest with an optional referral code.
 */
export type PasswordSignupRequest = {
  /**
   * Optional six-character customer or salesperson referral code; case-insensitive. Requires the referral program to be enabled.
   */
  referral_code?: string;
  email: string;
  password: string;
};

