/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * For target alarm provide alarm_at and omit head_id; for outbox provide
 * head_id and omit alarm_at. Copy comparison fields from fresh inspection.
 *
 */
export type DurableEntityRetryRequest = {
  namespace: string;
  key: string;
  environment?: string;
  platform_tenant_id?: string;
  target: 'alarm' | 'outbox';
  /**
   * Exact business uint64 version from inspection.
   */
  expected_version: number;
  /**
   * Opaque revision from the same inspection, invalidated by every manifest write.
   */
  expected_recovery_revision: string;
  head_id?: string;
  alarm_at?: string;
};

