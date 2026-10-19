/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A platform tenant the gateway refuses because of an overdue invoice.
 */
export type PlatformTenantOverdueHoldResponse = {
  tenant_id: string;
  /**
   * The oldest overdue invoice export holding the tenant.
   */
  export_id: string;
  due_at: string;
  /**
   * When the hold was placed.
   */
  since: string;
};

