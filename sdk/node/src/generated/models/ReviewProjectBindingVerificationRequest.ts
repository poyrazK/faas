/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CheckedProjectReleaseManifest } from './CheckedProjectReleaseManifest.js';
/**
 * Project release selection to review without creating receipts or starting verification.
 */
export type ReviewProjectBindingVerificationRequest = {
  manifest: CheckedProjectReleaseManifest;
  object_storage_write: boolean;
  queue_publish: boolean;
};

