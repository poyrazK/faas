/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One targeting rule of a platform flag. Accounts, group, plans and rollout combine with AND; the first matching rule decides.
 */
export type PlatformFlagRule = {
  id: string;
  accounts?: Array<string>;
  group?: string;
  plans?: Array<'free' | 'hobby' | 'pro' | 'scale'>;
  /**
   * Basis points of the accounts the other constraints admit.
   */
  rollout?: number;
  value: boolean;
};

