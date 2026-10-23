/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The full replacement set of notification channels for a budget's alerts (ADR-20261019193500).
 */
export type SetFinancialBudgetChannelsRequest = {
  /**
   * Account-owned notification channel ids; empty leaves only the account email.
   */
  channel_ids: Array<string>;
};

