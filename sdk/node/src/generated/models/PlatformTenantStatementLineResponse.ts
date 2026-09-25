/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable tenant-attributed UTC minute priced with one app's rate-card version. Exactly one of consumer_id or surface_id is present.
 */
export type PlatformTenantStatementLineResponse = {
  app_id: string;
  consumer_id?: string;
  surface_id?: string;
  window_start: string;
  billable_units: number;
  rate_card_id?: string;
  currency?: string;
  price_millicents_per_unit?: number;
  amount_millicents: number;
};

