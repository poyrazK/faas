/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Idempotent attachment of an existing managed object; bytes are checked before commit.
 */
export type OperationArtifactRequest = {
  name: string;
  /**
   * Managed object to verify before attachment, formatted as obj://<app UUID>/<bucket UUID>/<opaque key>.
   */
  uri: string;
  size_bytes: number;
  sha256: string;
  report_id: string;
};

