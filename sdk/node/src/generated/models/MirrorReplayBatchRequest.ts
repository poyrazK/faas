/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MirrorReplayRequestItem } from './MirrorReplayRequestItem.js';
/**
 * A bounded batch of sanitized historical requests to replay against the mirror deployment.
 */
export type MirrorReplayBatchRequest = {
  requests: Array<MirrorReplayRequestItem>;
  /**
   * Explicit acknowledgement required when any item uses POST, PUT, PATCH, or DELETE.
   */
  allow_unsafe_methods?: boolean;
};

