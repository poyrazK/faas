/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Preview; v2 only. Enables durable task steps, batch expansion and explicit retries. Submit exactly one of steps or batch. Requires FAAS_EXECUTION_PIPELINES_ENABLED plus v2 and tasks gates. Queueing and backoff count toward the deadline.
 */
export type ExecutionPipelineOptions = {
  total_timeout_ms?: number;
};

