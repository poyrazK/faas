/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Version 2 routing authority is granted by the account owner on the source. Customer identity is separate from untrusted event data. Business keys share a queue only within the same policy and customer scope; scalar types remain distinct. Canonical keys are limited to 256 bytes including the type prefix.
 */
export type CommitRouting = {
  version: 2;
  /**
   * Required for sources with allow_tenant_selection; forbidden otherwise. Must be active, in the source account and linked to the target app through an active surface.
   */
  platform_tenant_id?: string;
  key: (string | number | boolean);
};

