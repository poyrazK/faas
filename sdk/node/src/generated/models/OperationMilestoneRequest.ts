/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public application fact with a stable UUID reused across retries and recovery. Payloads are validated against the immutable definition; publication does not change business outcome.
 */
export type OperationMilestoneRequest = {
  id: string;
  name: string;
  /**
   * Declared JSON payload, bounded to 8192 UTF-8 bytes before and after canonicalization. The application chooses customer-visible data.
   */
  payload: any;
  /**
   * Application-recorded time. Normalized to UTC microsecond precision; independent of platform receipt time.
   */
  occurred_at: string;
};

