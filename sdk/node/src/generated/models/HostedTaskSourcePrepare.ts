/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostedTaskSourceEntry } from './HostedTaskSourceEntry.js';
import type { HostedTaskSourceReview } from './HostedTaskSourceReview.js';
/**
 * Exact reviewed Node Task bundles submitted for encrypted retention under an immutable caller-chosen identity.
 */
export type HostedTaskSourcePrepare = {
  review: HostedTaskSourceReview;
  sources: Record<string, Array<HostedTaskSourceEntry>>;
};

