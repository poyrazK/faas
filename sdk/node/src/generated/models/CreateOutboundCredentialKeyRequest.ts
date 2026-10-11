/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type CreateOutboundCredentialKeyRequest = {
  label: string;
  /**
   * Same format as the integration's primary credential; sealed for outboundd and never returned.
   */
  authorization: string;
};

