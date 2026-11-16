/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolQuarantineRequest } from './TaskToolQuarantineRequest.js';
import type { TaskToolReleaseExecutionRequest } from './TaskToolReleaseExecutionRequest.js';
/**
 * Fresh local fingerprint for drift detection or a separately authorized adapter claim transition.
 */
export type TaskToolReleaseIntentCheck = {
  quarantine?: TaskToolQuarantineRequest;
  release_fingerprint: string;
  execution?: TaskToolReleaseExecutionRequest;
};

