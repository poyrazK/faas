/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Which customers a run bills. With platform_tenants, a consumer's tenant-attributed usage is billed on the tenant statement and its app item is skipped. product_customers bills each Product Control customer's statement for the month and needs Product Control to be enabled.
 */
export type BillingRunSources = Array<'app_consumers' | 'platform_tenants' | 'product_customers'>;
