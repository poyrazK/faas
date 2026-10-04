/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProgressiveRollout } from './ProgressiveRollout.js';
/**
 * Ordered targeting rule; customer/group and subject constraints combine with AND. Subject rules require a customer or group constraint. Subject IDs are opaque application IDs and are evaluated only after application authentication. Boolean flags require a boolean value; variant flags may omit value to use weighted assignment. Progressive rollout is limited to boolean true rules.
 */
export type FlagRule = {
  id: string;
  customers?: Array<string>;
  /**
   * Owner-managed customer group key.
   */
  group?: string;
  /**
   * Opaque application subject IDs, matched inside the selected customers/group. Use stable internal IDs, not emails or display names.
   */
  subjects?: Array<string>;
  /**
   * Basis points of eligible customers by default, or eligible subjects when rollout_unit is subject; omitted means all eligible targets.
   */
  rollout?: number;
  /**
   * Allocation unit for rollout percentages. Omitted preserves customer-level allocation. Subject rollout requires a customer or group constraint and an authenticated subject context.
   */
  rollout_unit?: 'customer' | 'subject';
  progression?: ProgressiveRollout;
  value?: (boolean | string);
};

