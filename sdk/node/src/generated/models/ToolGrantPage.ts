/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolGrantRecord } from './ToolGrantRecord.js';
/**
 * Bounded owner-only grant page ordered by grant UUID.
 */
export type ToolGrantPage = {
  grants: Array<ToolGrantRecord>;
  next_cursor?: string;
};

