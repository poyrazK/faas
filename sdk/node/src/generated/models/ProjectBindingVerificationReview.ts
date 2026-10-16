/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckFinding } from './BindingCheckFinding.js';
import type { BindingVerificationReview } from './BindingVerificationReview.js';
import type { CheckedProjectReleaseManifest } from './CheckedProjectReleaseManifest.js';
/**
 * Read-only project verification plan, blockers, required consent and opaque review digests.
 */
export type ProjectBindingVerificationReview = {
  project: string;
  environment: string;
  reviewed_at: string;
  ready: boolean;
  review_digest: string;
  review_digests: Record<string, string>;
  manifest: CheckedProjectReleaseManifest;
  object_storage_write: boolean;
  queue_publish: boolean;
  object_storage_write_required: boolean;
  queue_publish_required: boolean;
  members: Record<string, BindingVerificationReview>;
  blockers: Array<BindingCheckFinding>;
};

