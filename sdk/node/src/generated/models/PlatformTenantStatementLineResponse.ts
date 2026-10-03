/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Compact immutable invoice line grouped by app, tenant-attributed consumer, surface, or JWT rule, and effective price source. window_start is the earliest included UTC minute; window_end is the exclusive end after the latest included minute and may span gaps. Exact minute coverage remains internal for additive revisions. Exactly one of consumer_id, surface_id, or jwt_authorization_rule_id is present.
 */
export type PlatformTenantStatementLineResponse = {
  app_id: string;
  consumer_id?: string;
  surface_id?: string;
  jwt_authorization_rule_id?: string;
  window_start: string;
  /**
   * Exclusive end after the latest included UTC minute; gaps inside the interval may have no usage.
   */
  window_end: string;
  billable_units: number;
  rate_card_id?: string;
  /**
   * Tenant-wide price source; mutually exclusive with rate_card_id.
   */
  platform_tenant_rate_card_id?: string;
  currency?: string;
  price_millicents_per_unit?: number;
  amount_millicents: number;
};

