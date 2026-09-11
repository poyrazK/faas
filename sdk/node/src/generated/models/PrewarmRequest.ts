/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Schedule temporary capacity restoration ahead of a demand window.
 */
export type PrewarmRequest = {
  /**
   * Desired number of live instances during the window; normal plan and ledger caps still apply.
   */
  count: number;
  /**
   * Start of the expected demand window.
   */
  wake_at: string;
  /**
   * End of the temporary intent; must be after wake_at.
   */
  expires_at: string;
};

