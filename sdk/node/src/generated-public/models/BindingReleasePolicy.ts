/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectStorageReleaseRequirements } from './ObjectStorageReleaseRequirements.js';
import type { QueueReleaseRequirements } from './QueueReleaseRequirements.js';
/**
 * Stored binding verification requirements and revision for an app deployment scope; unconfigured scopes default to off.
 */
export type BindingReleasePolicy = {
  queue_requirements?: QueueReleaseRequirements;
  object_storage_requirements?: ObjectStorageReleaseRequirements;
  app_id: string;
  scope: string;
  mode: 'off' | 'enforce';
  revision: number;
  /**
   * Canonical Go duration between 1s and 24h; default 10m0s.
   */
  max_verification_age: string;
  require_application_ack: boolean;
  updated_at?: string;
  /**
   * Recorded single-line change reason, at most 256 UTF-8 bytes.
   */
  reason?: string;
};

