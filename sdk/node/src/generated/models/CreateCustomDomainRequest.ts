/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bind an exact custom domain to an app, optionally following one of its project environments.
 */
export type CreateCustomDomainRequest = {
  domain: string;
  app_id: string;
  /**
   * Optional project environment slug. Wildcard hostnames cannot be environment-bound.
   */
  environment?: string;
};

