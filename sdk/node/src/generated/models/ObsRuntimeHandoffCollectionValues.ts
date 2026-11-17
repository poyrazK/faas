/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The reported collection-success bit and start of the last successful snapshot read, frozen on failure. A zero success report can also precede the first successful read.
 */
export type ObsRuntimeHandoffCollectionValues = {
  success: boolean;
  last_success_at?: string;
};

