/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { UnclaimedLimits } from './UnclaimedLimits.js';
/**
 * An anonymous deploy session.
 */
export type AnonymousSessionResponse = {
  account_id: string;
  /**
   * Deploy key, returned once. Use it as a bearer token.
   */
  api_key: string;
  /**
   * Give this link to the person the agent works for.
   */
  claim_url: string;
  /**
   * The account and its apps are deleted after this unless claimed.
   */
  expires_at: string;
  limits: UnclaimedLimits;
};

