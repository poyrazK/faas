/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A secret the account owner is asked to enter on a one-time page (ADR-20261026174727).
 */
export type SecretEntryRequest = {
  key: string;
  /**
   * Env scope; empty selects default.
   */
  scope?: string;
  /**
   * Shown to the owner on the entry page.
   */
  reason: string;
};

