/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Configuration that routes one trusted trigger through an exclusive-operation policy.
 */
export type ExclusiveTriggerBindingRequest = {
  policy: string;
  /**
   * Business coordination key; it never sets account or customer security scope.
   */
  key: (string | number | boolean);
  /**
   * Account-owner configured trusted tenant identity for tenant-scoped triggers.
   */
  platform_tenant_id?: string;
  /**
   * Required by join_existing policies; used to distinguish equivalent requests.
   */
  equivalence_key?: string;
};

