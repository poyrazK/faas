/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Persisted binding between an account-owned trigger or recurring Job schedule and its exclusive-operation policy. Exactly one of app_id or job_id is present.
 */
export type ExclusiveTriggerBindingRecord = {
  source: 'cron' | 'inbound_webhook' | 'broker' | 'job_schedule';
  trigger_id: string;
  /**
   * Present for app-owned triggers.
   */
  app_id?: string;
  /**
   * Present for a recurring Job schedule.
   */
  job_id?: string;
  policy: string;
  platform_tenant_id?: string;
  key: (string | number | boolean);
  equivalence_key?: string;
  created_at: string;
  updated_at: string;
};

