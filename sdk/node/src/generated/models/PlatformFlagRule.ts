/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One targeting rule of a platform flag. Accounts, orgs, group, plans and rollout combine with AND; the first matching rule decides. Orgs match the organization a request acts under (ADR 20261021080640).
 */
export type PlatformFlagRule = {
  id: string;
  accounts?: Array<string>;
  orgs?: Array<string>;
  group?: string;
  plans?: Array<'free' | 'hobby' | 'pro' | 'scale'>;
  /**
   * Basis points of the accounts the other constraints admit.
   */
  rollout?: number;
  value: boolean;
};

