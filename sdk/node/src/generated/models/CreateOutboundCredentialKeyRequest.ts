/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An extra provider key to seal for the outbound gateway, with a DNS-label label.
 */
export type CreateOutboundCredentialKeyRequest = {
  label: string;
  /**
   * Same format as the integration's primary credential; sealed for the outbound gateway and never returned.
   */
  authorization: string;
};

