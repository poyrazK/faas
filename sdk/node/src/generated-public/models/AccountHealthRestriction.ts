/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One account restriction in customer wording, with the next step when there is one.
 */
export type AccountHealthRestriction = {
  /**
   * Stable restriction identifier.
   */
  kind: 'account_suspended' | 'account_past_due' | 'deletion_pending' | 'abuse_hold' | 'email_unverified' | 'overage_cap_reached' | 'platform_release_pause';
  /**
   * Short headline for the restriction.
   */
  title: string;
  /**
   * What the restriction does to the account.
   */
  detail: string;
  /**
   * Label of the next step.
   */
  action?: string;
  /**
   * Dashboard path for the next step.
   */
  action_url?: string;
  /**
   * When the restriction started, when known.
   */
  since?: string;
};

