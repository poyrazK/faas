/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Canonical org wire shape. RFC3339 timestamps; zero-time
 * serialises as empty string. Mirrors `pkg/api.OrgResponse`.
 *
 */
export type OrgResponse = {
  /**
   * Org UUID (stable across renames).
   */
  id: string;
  /**
   * Lowercase slug. Personal orgs use `u-<12hex>`.
   */
  slug: string;
  name: string;
  /**
   * True iff this is the caller's personal org (every account has exactly one).
   */
  personal: boolean;
  /**
   * Plan tier. Personal orgs default to `free`.
   */
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  status: 'active' | 'past_due' | 'suspended' | 'deleted_pending';
  created_at: string;
  updated_at: string;
};

