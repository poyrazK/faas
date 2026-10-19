/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationQualification } from './BindingVerificationQualification.js';
import type { BindingVerificationRun } from './BindingVerificationRun.js';
import type { CheckedProjectReleaseManifest } from './CheckedProjectReleaseManifest.js';
/**
 * Durable project verification graph with pinned members, a shared budget and a fixed deadline.
 */
export type ProjectBindingVerificationRun = {
  qualification?: BindingVerificationQualification;
  /**
   * Project-wide admission budget frozen on this graph receipt and shared by every member run.
   */
  max_concurrency?: number;
  id: string;
  project: string;
  environment: string;
  status: 'pending' | 'passed' | 'blocked' | 'cancelled' | 'expired';
  reason?: string;
  created_at: string;
  expires_at: string;
  review_digest: string;
  manifest: CheckedProjectReleaseManifest;
  object_storage_write: boolean;
  queue_publish: boolean;
  members: Record<string, BindingVerificationRun>;
};

