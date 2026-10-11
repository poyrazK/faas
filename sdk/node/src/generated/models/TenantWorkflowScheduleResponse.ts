/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A tenant's effective cadence for an opted-in published schedule workflow.
 */
export type TenantWorkflowScheduleResponse = {
  workflow_name: string;
  deployment_id: string;
  /**
   * Five-field cron expression.
   */
  schedule: string;
  /**
   * Effective IANA timezone.
   */
  timezone: string;
  overlap: 'skip' | 'allow';
  enabled: boolean;
  tenant_configurable: boolean;
  /**
   * Whether this tenant has saved an override.
   */
  customized: boolean;
  /**
   * Zero uses the published defaults; use this value as expected_version when updating.
   */
  version: number;
};

