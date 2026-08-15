/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PATCH /v1/jobs/{id} body. Every field is optional;
 * absent = leave unchanged. Pointer semantics on the
 * wire: `null` means "leave unchanged" for the explicit
 * `null` fields, while omitting the key entirely is
 * also "leave unchanged" — both are indistinguishable on
 * the JSON wire.
 *
 */
export type UpdateJobRequest = {
  image_ref?: string;
  ram_mb?: number;
  task_timeout_s?: number;
  max_parallelism?: number;
  retry_max?: number;
  env_overrides?: Record<string, string>;
};

