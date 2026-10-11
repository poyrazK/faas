/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Tenant-owned schedule settings. Workflow input and workflow steps cannot be changed here.
 */
export type UpdateTenantWorkflowScheduleRequest = {
  /**
   * Zero creates the first override; otherwise use the latest version returned by list or update.
   */
  expected_version: number;
  /**
   * Cron expression evaluated with the platform's five-field grammar.
   */
  schedule: string;
  /**
   * IANA timezone; omitted uses the published timezone or UTC.
   */
  timezone?: string;
  /**
   * Omitted uses the published overlap behavior or skip.
   */
  overlap?: 'skip' | 'allow';
  /**
   * Whether this tenant's schedule is active.
   */
  enabled?: boolean;
};

