/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Invocation } from './Invocation.js';
/**
 * Page of invocations; ordered by created_at DESC, id DESC. When present, pass next_before as `?before=` to load older rows.
 */
export type ListInvocationsResponse = {
  invocations: Array<Invocation>;
  /**
   * ID cursor for the next older page; omitted when this page contains fewer rows than requested.
   */
  next_before?: string;
};

