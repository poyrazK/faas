/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountRiskEvent } from './AccountRiskEvent.js';
import type { AccountRiskLink } from './AccountRiskLink.js';
import type { AccountRiskSignup } from './AccountRiskSignup.js';
import type { AccountRiskStrike } from './AccountRiskStrike.js';
/**
 * An account's abuse risk. email only with include_pii.
 */
export type AccountRiskResponse = {
  account_id: string;
  email?: string;
  status: string;
  plan: string;
  created_at: string;
  abuse_hold_at?: string;
  abuse_hold_reason?: string;
  signup?: AccountRiskSignup;
  links_to_held: Array<AccountRiskLink>;
  linked_accounts: Array<AccountRiskLink>;
  strikes: Array<AccountRiskStrike>;
  abuse_events: Array<AccountRiskEvent>;
};

