/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DelayedTaskAfterRequest } from './DelayedTaskAfterRequest.js';
import type { DelayedTaskAtRequest } from './DelayedTaskAtRequest.js';
/**
 * Body for POST /v1/apps/{slug}/delayed-tasks. Supply exactly one scheduling form; the maximum delay is 31,536,000 seconds (365 days).
 */
export type DelayedTaskRequest = (DelayedTaskAtRequest | DelayedTaskAfterRequest);

