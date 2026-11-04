/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A package whose presence or version differs between the builds.
 */
export type ReleaseDiffPackage = {
  /**
   * The package's purl without its version, or its group and name.
   */
  name: string;
  from?: string;
  to?: string;
};

