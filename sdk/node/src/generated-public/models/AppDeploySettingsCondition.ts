/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Atomic preconditions for single-app lifecycle/scaling recovery. The app must retain this identity and its newest deployment must be the expected live deployment. Every targeted field must still match its observed value, including targeted fields omitted from the partial update because they already match the desired value. Checked in the same transaction as the update. A conflict returns 409 deploy_settings_conflict with field names, without setting values. Cannot be combined with environment edits or other app fields. Ordinary updates can omit this object.
 */
export type AppDeploySettingsCondition = {
  /**
   * Exact app ID returned by the app read.
   */
  expected_app_id: string;
  /**
   * Exact accepted deployment being recovered.
   */
  expected_deployment_id: string;
  /**
   * Observed values from the app read; null represents an unset optional policy. Must cover every mutated setting.
   */
  expected_settings: Record<string, any>;
};

