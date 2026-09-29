/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountAbuseHold } from './AccountAbuseHold.js';
/**
 * An account's abuse hold after an operator action.
 */
export type AccountAbuseHoldActionResponse = {
  account_id: string;
  /**
   * The hold after the action; null when the account is not held.
   */
  abuse_hold: (AccountAbuseHold | null);
  changed: boolean;
};

