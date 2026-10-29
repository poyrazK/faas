/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact published runtime base bytes; host kernel and function runner are separate components.
 */
export type RuntimeReleaseResponse = {
  id: string;
  runtime: 'node22' | 'node24' | 'python312' | 'python313' | 'go124' | 'go124-alpine';
  architecture: 'amd64' | 'arm64';
  /**
   * Immutable OCI source manifest digest.
   */
  source_digest: string;
  /**
   * SHA-256 of the PID 1 binary injected into this base.
   */
  guest_init_digest: string;
  /**
   * SHA-256 of the exact published ext4 bytes.
   */
  base_digest: string;
  layout_version: string;
  published_at: string;
  /**
   * Publication alone establishes no upgrade compatibility verdict.
   */
  qualification: 'not_evaluated';
};

