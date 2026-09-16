/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One regular file in an ephemeral execution bundle. Content is base64-encoded in JSON.
 */
export type ExecutionFile = {
  /**
   * Normalized relative POSIX path; absolute paths, dot segments, and symlinks are rejected.
   */
  path: string;
  /**
   * File bytes, base64-encoded by JSON clients.
   */
  content: string;
};

