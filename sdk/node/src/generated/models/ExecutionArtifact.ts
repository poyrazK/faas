/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An explicitly exported file. Inline base64 content shares the JSON output budget. Stored artifacts contain a file_id, null content, and independently budgeted bytes accessible through authenticated file endpoints.
 */
export type ExecutionArtifact = {
  /**
   * Authenticated retained file locator for stored artifacts.
   */
  file_id?: string;
  name: string;
  size_bytes: number;
  sha256: string;
  /**
   * File bytes encoded as base64.
   */
  content: string | null;
};

