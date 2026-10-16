/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingVerificationQualification } from './BindingVerificationQualification.js';
import type { BindingVerificationRun } from './BindingVerificationRun.js';
import type { CheckedProjectReleaseManifest } from './CheckedProjectReleaseManifest.js';
export type ProjectBindingVerificationRun = {
  qualification?: BindingVerificationQualification;
  /**
   * Frozen shared admission budget; omitted values use two. Graph members share the root budget.
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

