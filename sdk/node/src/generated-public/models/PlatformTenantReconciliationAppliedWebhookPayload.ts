/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Successful apply notification with the platform owner's stable tenant reference. Use receipt_id to fetch full changes; the event omits the submitted desired bundle and hostname challenge tokens.
 */
export type PlatformTenantReconciliationAppliedWebhookPayload = {
  platform_tenant_id: string;
  external_ref: string;
  receipt_id: string;
  plan_hash: string;
  applied_at: string;
  change_count: number;
};

