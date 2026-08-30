/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of POST /v1/uploads. `total_size` must be ≤ the per-plan SourceTarballMaxMB cap (Free/Hobby 100 MB, Pro/Scale 250 MB); the handler returns 413 + `source_too_large` otherwise. `sha256_hex` is recorded for the build_provenance audit row only — the server does NOT re-verify it at commit time (ADR-115 trust boundary).
 */
export type UploadStartRequest = {
  app_slug: string;
  /**
   * Total tarball bytes after gzip. Hard ceiling is 1 GiB; per-plan cap is enforced by the handler.
   */
  total_size: number;
  /**
   * Optional sha256 of the tarball for build_provenance. Server does not verify.
   */
  sha256_hex?: string | null;
};

