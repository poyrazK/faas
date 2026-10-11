/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Build source within the approved Git tree, a function runner, or an immutable OCI digest. Function sources require runtime and exclude dockerfile. The gated internal executor reserves new private workloads when app is omitted. Preparation does not grant serving authority.
 */
export type EnvironmentWorkloadSource = {
  kind: 'source' | 'dockerfile' | 'image' | 'function';
  /**
   * Supported runner; required only when kind is function.
   */
  runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313';
  directory?: string;
  dockerfile?: string;
  image?: string;
};

