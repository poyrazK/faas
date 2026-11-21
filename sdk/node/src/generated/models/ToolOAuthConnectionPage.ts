/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolOAuthConnectionRecord } from './ToolOAuthConnectionRecord.js';
/**
 * Owner-scoped page of retained execution OAuth metadata and a continuation UUID.
 */
export type ToolOAuthConnectionPage = {
  connections: Array<ToolOAuthConnectionRecord>;
  next_cursor?: string;
};

