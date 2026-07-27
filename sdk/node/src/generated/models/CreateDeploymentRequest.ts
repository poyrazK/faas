/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Two content-types accepted (see operation description): prebuilt OCI image reference, or multipart source upload.
 */
export type CreateDeploymentRequest = {
  /**
   * registry.DOMAIN/...@sha256:... — digest-pinned OCI reference.
   */
  image?: string;
};

