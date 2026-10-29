/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A platform tenant's free trial: its usage in [starts_at, ends_at) is priced at zero.
 */
export type PlatformTenantTrialResponse = {
  tenant_id: string;
  starts_at: string;
  ends_at: string;
  /**
   * False once the tenant's trial has ended.
   */
  active: boolean;
};

