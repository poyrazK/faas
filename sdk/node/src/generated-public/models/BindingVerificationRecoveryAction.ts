/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Suggested client recovery step for a verification receipt; it grants no execution authority.
 */
export type BindingVerificationRecoveryAction = {
  action: 'resume' | 'wait' | 'wait_for_application_ack' | 'cancel' | 'review_new_run' | 'retry_diagnostics';
  message: string;
};

