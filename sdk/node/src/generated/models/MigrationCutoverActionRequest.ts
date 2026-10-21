/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit user action. Restored records the customer attestation that provider DNS was restored; Gregale never edits external DNS.
 */
export type MigrationCutoverActionRequest = {
  action: 'start_checks' | 'complete' | 'cancel' | 'confirm_restored';
  acknowledge_limited_traffic: boolean;
};

