/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MirrorReplayInvocation } from './MirrorReplayInvocation.js';
/**
 * The replay invocations accepted and queued for asynchronous mirror execution.
 */
export type MirrorReplayBatchResponse = {
  queued: number;
  invocations: Array<MirrorReplayInvocation>;
};

