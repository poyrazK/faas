/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * PUT body for /v1/apps/{slug}/network/private.
 */
export type AppPrivateNetworkAttachmentRequest = {
  network_id: string;
  region: string;
  cidrs: Array<string>;
};

