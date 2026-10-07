/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Supply either a request-only regional account-admin key or the original active runtime consent UUID, never both.
 */
export type DeliverGlobalRuntimeQuotaRequest = {
  regional_api_key?: string;
  runtime_consent_id?: string;
};

