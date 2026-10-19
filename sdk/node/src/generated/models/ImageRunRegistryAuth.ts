/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-1790: credential for a private image's registry. apid stores it sealed on the image's runner job for that registry host, exactly as PUT /v1/jobs/{name}/registry-credentials does, and never returns it. Every run that supplies it refreshes it.
 */
export type ImageRunRegistryAuth = {
  /**
   * Registry account or robot name for the image's registry.
   */
  username: string;
  /**
   * Registry password or access token; prefer a short-lived read-only token.
   */
  password: string;
};

