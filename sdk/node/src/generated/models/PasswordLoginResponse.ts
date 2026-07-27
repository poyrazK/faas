/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Successful sign-in. The session cookie is set via
 * `Set-Cookie: faas_sid=…`; the body carries only
 * `{account_id, plan}`. No API key is ever returned on
 * login (issue #165, PR #2).
 *
 */
export type PasswordLoginResponse = {
  account_id: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
};

