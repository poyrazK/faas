/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Path that primes a deployment's first init snapshot. Before that capture Gregale sends up to 20 GET requests (5 s at most) to it on the app's HTTP port; without it, the HTTP health check path is used. Priming never fails a deploy.
 */
export type WarmupHook = {
  /**
   * Absolute path on the app's HTTP port. Should exercise the request code a wake needs warm and be safe to call repeatedly.
   */
  path?: string;
};

