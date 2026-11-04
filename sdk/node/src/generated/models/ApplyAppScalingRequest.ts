/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A reviewed partial scaling change with a durable request identity.
 */
export type ApplyAppScalingRequest = {
  /**
   * Immutable app UUID from the reviewed scaling snapshot.
   */
  expected_app_id: string;
  request_id: string;
  /**
   * Required for a named environment; must match its immutable identity.
   */
  expected_environment_id?: string;
  expected_revision: number;
  ram_mb?: number;
  cpu_millicores?: number;
  max_concurrency?: number;
  min_instances?: number;
};

