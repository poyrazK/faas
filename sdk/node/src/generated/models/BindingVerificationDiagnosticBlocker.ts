/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type BindingVerificationDiagnosticBlocker = {
  code: string;
  message: string;
  app?: string;
  type?: string;
  name?: string;
  binding?: string;
  scope?: string;
  status?: string;
  recovery: 'resume' | 'wait' | 'wait_for_application_ack' | 'cancel' | 'review_new_run' | 'retry_diagnostics';
  step_index?: number;
  resource_id?: string;
  resource_kind?: 'task' | 'queue';
};

