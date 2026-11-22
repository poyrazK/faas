/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One durable mutation; reuse the ID for retries of the same key and selector.
 */
export type ObjectDeletionRequest = {
  id: string;
  key: string;
  /**
   * Omit for ordinary deletion; use null or an owned public version UUID for permanent deletion.
   */
  version_id?: string;
};

