/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InstanceResponse } from './InstanceResponse.js';
/**
 * Page shape for `GET /v1/instances` (issue #393). `instances`
 * is the page in started_at DESC, id DESC order. `next_before`
 * is the cursor the caller passes on the next request to fetch
 * the older page; empty when the page is the end.
 *
 */
export type ListInstancesResponse = {
  instances: Array<InstanceResponse>;
  /**
   * Cursor (instances.id UUIDv7) for the next older page. Empty / null at the end.
   */
  next_before?: string | null;
};

