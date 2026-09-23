/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A repository-declared dependency on another same-account app. The binding is injected for discovery and becomes an authorization capability when the caller selects the `declared` service binding policy.
 */
export type AppServiceBinding = {
  /**
   * Platform-owned environment key containing the internal service URL.
   */
  binding: string;
  /**
   * Stable target app name used by private service discovery.
   */
  service: string;
};

