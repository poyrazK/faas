/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Mutable plaintext environment values from one authorized app scope, excluding sealed secrets.
 */
export type AppEnvExportResponse = {
  app_slug: string;
  scope: string;
  /**
   * Mutable plaintext env values only. Never includes sealed secrets.
   */
  values: Record<string, string>;
};

