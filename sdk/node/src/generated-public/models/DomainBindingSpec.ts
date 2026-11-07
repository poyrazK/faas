/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A desired exact or wildcard hostname, app slug and optional project environment. An omitted environment selects ordinary default routing.
 */
export type DomainBindingSpec = {
  domain: string;
  app: string;
  environment?: string;
};

