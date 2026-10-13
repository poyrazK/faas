/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CommitRouting } from './CommitRouting.js';
/**
 * At-least-once handoff. Repeating the identity with identical JSON data and routing returns the original receipt; changed type, schema version, data or routing conflicts.
 */
export type CommitEventRequest = {
  id: string;
  type: string;
  data: any;
  routing?: CommitRouting;
  /**
   * Optional registered application event schema version. Only event destinations support this field.
   */
  schema_version?: string;
};

