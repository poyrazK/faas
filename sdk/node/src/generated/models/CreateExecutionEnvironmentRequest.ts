/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable public language dependencies and exact Wolfi system package pins built inside an isolated builder VM.
 */
export type CreateExecutionEnvironmentRequest = {
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  /**
   * Exact NAME=VERSION-rN Wolfi package pins. Uses only the installer image repositories. May be used without language dependencies; direct pins and the installer image enter the recipe identity.
   */
  system_packages?: Array<string>;
  /**
   * Exact Python pins and SHA-256 hashes. All transitive dependencies must be pinned. No URLs or index options.
   */
  requirements?: string;
  /**
   * npm dependency manifest as JSON text. No workspace or registry overrides.
   */
  package_json?: string;
  /**
   * npm lockfile v3 as JSON text; public npm registry and SHA-512 integrity only. The combined recipe is at most 1 MiB.
   */
  package_lock?: string;
};

