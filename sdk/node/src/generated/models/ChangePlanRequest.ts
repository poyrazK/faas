/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Target plan for the change.
 */
export type ChangePlanRequest = {
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  /**
   * Optional salesperson code for a personal account's first paid purchase.
   * Letters are normalized to uppercase and surrounding spaces are removed.
   * Requires a live verified session. Only active sales codes are accepted;
   * existing referrals and prior payments cannot be reassigned. The choice
   * remains saved if checkout is retried or abandoned. Omit or leave empty
   * to keep ordinary billing behavior. Organization purchases are excluded.
   *
   */
  salesperson_code?: string;
};

