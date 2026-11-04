/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Persisted line coverage after refreshing an existing invoice from its billing provider.
 */
export type InvoiceRefreshResponse = {
  invoice_id: string;
  provider: 'stripe' | 'paddle' | 'polar';
  /**
   * Whether the refreshed line snapshot is complete, classified, and exactly reconciled.
   */
  detailed: boolean;
  line_items: number;
  /**
   * Reason for aggregate fallback; omitted when detailed is true.
   */
  source_gap?: 'unavailable' | 'incomplete' | 'empty' | 'unclassified' | 'tax_in_non_tax_lines' | 'totals_mismatch';
  updated_at: string;
};

