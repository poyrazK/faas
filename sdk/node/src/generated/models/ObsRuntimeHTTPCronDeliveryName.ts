/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Closed producer outcome. success is a settled successful attempt; retry is a retry disposition without a final result; app_error is an application-error classification. failed and dead_letter remain distinct delivery-failure classifications. Scheduled fires and manual runs share this metric.
 */
export type ObsRuntimeHTTPCronDeliveryName = 'success' | 'retry' | 'app_error' | 'failed' | 'dead_letter';
