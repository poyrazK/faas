/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskReleaseReview } from './TaskReleaseReview.js';
import type { TaskToolReleaseApp } from './TaskToolReleaseApp.js';
/**
 * Canonical hosted source review, including source digests and native app baselines.
 */
export type HostedTaskSourceReview = (TaskReleaseReview & {
  kind: 'hosted_task_source_review';
  account_id: string;
  source_sha256: {
    web: string;
    worker: string;
  };
  apps: Array<TaskToolReleaseApp>;
});

