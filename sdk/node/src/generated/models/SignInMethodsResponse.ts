/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { LinkedSignIn } from './LinkedSignIn.js';
/**
 * The ways the account can sign in.
 */
export type SignInMethodsResponse = {
  password: boolean;
  mfa: boolean;
  providers: Array<LinkedSignIn>;
};

