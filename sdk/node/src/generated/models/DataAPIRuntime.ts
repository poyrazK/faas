/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The Data API runtime the control plane serves (ADR-20261031095954).
 */
export type DataAPIRuntime = {
  /**
   * Digest of the runtime's image files; running Data APIs report it as runtime_version on /healthz.
   */
  version: string;
  /**
   * Lowercase hex SHA-256 of the decoded archive.
   */
  archive_sha256: string;
  /**
   * Base64 gzipped tar of the runtime under one data-api/ directory, at most 2 MiB decoded.
   */
  archive: string;
};

