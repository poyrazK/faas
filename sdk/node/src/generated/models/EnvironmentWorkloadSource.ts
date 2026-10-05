/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Build source within the approved Git tree or an immutable OCI digest.
 */
export type EnvironmentWorkloadSource = {
  kind: 'source' | 'dockerfile' | 'image';
  directory?: string;
  dockerfile?: string;
  image?: string;
};

