/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact native candidate and catalog pin with the reviewed transient behavioral fixture bytes.
 */
export type ToolPublicationVerificationRequest = {
  deployment_id: string;
  catalog_sha256: string;
  /**
   * Exact reviewed fixture bytes; SHA-256 must match the publication intent
   */
  fixtures_json: string;
};

