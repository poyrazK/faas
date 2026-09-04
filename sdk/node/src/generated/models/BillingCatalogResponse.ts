/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingCatalogEntry } from './BillingCatalogEntry.js';
/**
 * Wire shape for GET / POST / DELETE
 * /v1/admin/billing-paddle-catalog compatibility endpoints
 * (PR-P3). Provider is the active billing provider's name
 * (polar / paddle); providers without a catalog surface
 * return 501. SyncedAt is the timestamp of the most recent
 * successful catalog preflight; empty when no hydration has
 * completed.
 *
 */
export type BillingCatalogResponse = {
  /**
   * Active provider name (polar / paddle).
   */
  provider: string;
  /**
   * RFC 3339 last-sync timestamp; empty string when never synced.
   */
  synced_at: string;
  entries: Array<BillingCatalogEntry>;
};

