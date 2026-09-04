/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row in the provider price + product catalog (PR-P3).
 * Plan values are the billable api.Plan constants
 * ("hobby", "pro", "scale") — PlanFree is intentionally
 * absent because it carries no recurring line item.
 * Handle is the provider-side product or price ID. SyncedAt is
 * RFC 3339 UTC from the catalog's last successful preflight.
 *
 */
export type BillingCatalogEntry = {
  plan: 'hobby' | 'pro' | 'scale';
  kind: 'monthly' | 'overage' | 'product';
  /**
   * Provider price or product ID.
   */
  handle: string;
  synced_at: string;
};

