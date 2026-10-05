/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CommitRouting } from './CommitRouting.js';
/**
 * At-least-once handoff. Repeating the identity with identical JSON data and routing returns the original receipt; changed type, data or routing conflicts.
 */
export type CommitEventRequest = {
  id: string;
  type: string;
  data: any;
  routing?: CommitRouting;
};

