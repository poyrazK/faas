/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The customer's consumer and new key, shown only here.
 */
export type CompleteConsumerSignupResponse = {
  consumer_id: string;
  key: string;
  key_prefix: string;
  scopes: Array<string>;
  plan_id?: string;
  trial_ends_at?: string;
  return_url?: string;
};

