/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Logical object data and an owned public version selector; native provider identifiers remain private.
 */
export type ObjectVersion = {
  key: string;
  version_id: string;
  is_latest: boolean;
  delete_marker: boolean;
  size_bytes: number;
  etag?: string;
  last_modified: string;
  storage_class?: string;
};

