/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Job private-registry credential payload. Password is sealed at rest and never returned.
 */
export type PutJobRegistryCredentialRequest = {
  /**
   * HTTPS registry host, optionally with port.
   */
  registry: string;
  username: string;
  password: string;
};

