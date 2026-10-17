/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { QueueReleaseRequirements } from './QueueReleaseRequirements.js';
import type { SetBindingReleasePolicyRequest } from './SetBindingReleasePolicyRequest.js';
/**
 * Revision-guarded release policy replacement with explicit per-binding queue readiness requirements and optional storage requirements.
 */
export type SetQueueBindingReleasePolicyRequest = (SetBindingReleasePolicyRequest & {
  queue_requirements: QueueReleaseRequirements;
});

