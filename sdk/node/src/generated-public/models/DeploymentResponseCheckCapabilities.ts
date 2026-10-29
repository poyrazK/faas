/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Installed deployment-check protocol and forwarding configuration; no workload admission is performed.
 */
export type DeploymentResponseCheckCapabilities = {
  /**
   * Protocol implemented by this API; clients require a compatible version.
   */
  protocol_version: number;
  available: boolean;
  /**
   * Stable gateway_unsupported, gateway_unconfigured, or gateway_unavailable code when unavailable.
   */
  unavailable_reason?: string;
};

