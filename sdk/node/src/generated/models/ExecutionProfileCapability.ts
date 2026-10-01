/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One dependency profile and its accepted runtime versions.
 */
export type ExecutionProfileCapability = {
  profile: 'standard' | 'python-data-v1';
  runtimes: Array<'node22' | 'node24' | 'python312' | 'python313'>;
  /**
   * Immutable preinstalled package versions, when the profile includes packages.
   */
  packages?: Record<string, string>;
};

