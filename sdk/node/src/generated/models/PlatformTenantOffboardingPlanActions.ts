/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts and ownership-scoped changes that would be applied if the offboarding plan is confirmed.
 */
export type PlatformTenantOffboardingPlanActions = {
  suspend_tenant: boolean;
  revoke_consumer_keys: number;
  revoke_access_tokens: number;
  detach_managed_consumers: number;
  retain_unmanaged_consumers: number;
  detach_managed_surfaces: number;
  retain_unmanaged_surfaces: number;
  remove_managed_hostnames: number;
  retain_unmanaged_hostnames: number;
  disable_credential_delegation: boolean;
  disable_customer_provisioning: boolean;
  disable_hostname_delegation: boolean;
  preserve_usage_history: boolean;
  preserve_billing_statements: boolean;
  preserve_reconciliation_history: boolean;
  preserve_webhook_subscriptions: boolean;
};

