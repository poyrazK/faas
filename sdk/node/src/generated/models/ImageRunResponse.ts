/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppTaskAttachInfo } from './AppTaskAttachInfo.js';
/**
 * Answer to POST /v1/image-runs. job names the runner job; run_id addresses /v1/jobs/{job}/runs/{run_id} once status is queued.
 */
export type ImageRunResponse = {
  status: 'pulling' | 'queued';
  /**
   * Runner job name (ir-<hash>).
   */
  job: string;
  /**
   * Normalized image reference.
   */
  image: string;
  image_status: 'pending' | 'verifying_legacy' | 'ready' | 'failed';
  /**
   * Last pull or build error while the image is retried.
   */
  image_error?: string;
  /**
   * Resolved manifest digest once ready.
   */
  image_digest?: string;
  run_id?: string;
  /**
   * The resolved argv the task runs.
   */
  command?: Array<string>;
  /**
   * One-time attach credential of an interactive run, returned only by the response that created the run.
   */
  attach?: AppTaskAttachInfo;
};

