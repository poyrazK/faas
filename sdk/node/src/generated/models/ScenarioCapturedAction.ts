/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A managed operation or workflow webhook intent captured at completion. Runtime IDs support inspection; comparison uses logical workloads and committed business data.
 */
export type ScenarioCapturedAction = {
  id: string;
  workload: string;
  source: 'operation' | 'workflow';
  operation_id: string;
  workflow?: string;
  step?: string;
  name: string;
  webhook_id: string;
  /**
   * Fingerprint of destination scope, business tenant reference, exact URL, delivery format and retry policy observed at completion. URLs and secrets are not exported.
   */
  receiver_sha256: string;
  type: string;
  /**
   * Committed JSON business data bounded to 64 KiB per intent.
   */
  payload: any;
};

