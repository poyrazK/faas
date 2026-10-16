/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CheckedProjectReleaseManifest } from './CheckedProjectReleaseManifest.js';
export type CreateProjectBindingVerificationRunRequest = {
  /**
   * Frozen shared admission budget; omitted values use two. Graph members share the root budget.
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

