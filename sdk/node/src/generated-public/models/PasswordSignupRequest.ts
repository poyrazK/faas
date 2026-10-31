/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Email + password for signup. Same shape as PasswordLoginRequest so the create-or-claim race detection reuses the verifier.
 */
export type PasswordSignupRequest = {
  email: string;
  password: string;
};

