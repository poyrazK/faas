/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Captured identity responsible for an activity item.
 */
export type ActivityActorResponse = {
  type: 'user' | 'api_key' | 'github' | 'system' | 'operator';
  /**
   * Actor name captured when the activity occurred.
   */
  label: string;
  /**
   * Present for a locally-known human actor.
   */
  account_id?: string;
};

