/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CheckedProjectReleaseManifest } from './CheckedProjectReleaseManifest.js';
/**
 * Reviewed project selection, consent and shared concurrency for creating a durable verification graph.
 */
export type CreateProjectBindingVerificationRunRequest = {
  /**
   * Requested shared graph admission budget; defaults to two and applies across all project members.
   */
  max_concurrency?: number;
  /**
   * Optional aggregate digest issued by graph review; when supplied, manifest, member digests and consent must match.
   */
  review_digest?: string;
  id: string;
  manifest: CheckedProjectReleaseManifest;
  review_digests: Record<string, string>;
  ttl_seconds: number;
  object_storage_write: boolean;
  queue_publish: boolean;
};

