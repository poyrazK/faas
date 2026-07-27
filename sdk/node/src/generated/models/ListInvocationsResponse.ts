/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Invocation } from './Invocation.js';
/**
 * Page of invocations; ordered by created_at DESC, id DESC. Pass the LAST id of the returned slice as the next `?before=` to load older.
 */
export type ListInvocationsResponse = {
  invocations: Array<Invocation>;
};

