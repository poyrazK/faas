/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The billing subject's election to pay for egress above its plan's monthly
 * allowance instead of stopping at it (ADR-20261104204500).
 *
 */
export type EgressOverageResponse = {
  /**
   * Whether the election is on.
   */
  enabled: boolean;
  /**
   * Whether the plan allows the election; Free does not.
   */
  electable: boolean;
  /**
   * When the election started; kept when it is turned off and on again.
   */
  enabled_at?: string;
  /**
   * When the election was last turned off.
   */
  disabled_at?: string;
  /**
   * The plan's monthly egress allowance in GiB.
   */
  included_gib: number;
  /**
   * live when egress above the allowance is billed while the election is on; off while egress is not billed, when the allowance still holds.
   */
  billing: 'live' | 'off';
  /**
   * Price of a GiB above the allowance while billing is live, in millicents.
   */
  price_millicents_per_gib?: number;
  /**
   * Currency of the price.
   */
  currency?: string;
};

