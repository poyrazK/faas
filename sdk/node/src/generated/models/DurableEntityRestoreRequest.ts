/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DurableEntityStateExport } from './DurableEntityStateExport.js';
export type DurableEntityRestoreRequest = {
  namespace: string;
  key: string;
  environment?: string;
  platform_tenant_id?: string;
  request_id: string;
  /**
   * Registered validator bundle digest. Required for isolated restores.
   */
  validation_bundle_sha256?: string;
  /**
   * Explicit deployment pin. Required for restores when application validation is enabled.
   */
  validation_deployment_id?: string;
  expected_version: number;
  export: DurableEntityStateExport;
};

