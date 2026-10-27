/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A Compose service's deploy.replicas and deploy.resources.limits, converted to app settings. Omitted fields are undeclared and keep the app's own settings.
 */
export type ComposeResources = {
  /**
   * Caps a request-mode app's instances (max_concurrency) and fixes a worker's or service's replica count.
   */
  replicas?: number;
  /**
   * App RAM in MB, rounded up from the declared size.
   */
  memory_mb?: number;
  /**
   * App CPU share.
   */
  cpu_millicores?: 250 | 500 | 1000;
};

