/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppResourceInventoryBinding } from './AppResourceInventoryBinding.js';
/**
 * Allowlisted resource metadata; optional fields depend on the page kind.
 */
export type AppResourceInventoryItem = {
  id: string;
  app_id: string;
  scope: string;
  name?: string;
  domain?: string;
  verified?: boolean;
  cert_status?: string;
  schedule?: string;
  path?: string;
  timezone?: string;
  enabled?: boolean;
  region?: string;
  state?: string;
  postgres_major?: number;
  bucket_id?: string;
  bucket?: string;
  prefix?: string;
  rotation_pending?: boolean;
  binding?: AppResourceInventoryBinding;
};

