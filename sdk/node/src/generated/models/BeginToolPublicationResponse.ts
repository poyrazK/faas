/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolPublicationRecord } from './ToolPublicationRecord.js';
/**
 * Journal insertion receipt; only a new insertion can authorize one publisher launch.
 */
export type BeginToolPublicationResponse = {
  /**
   * True only for the first successful journal insertion; never cached as execution permission
   */
  created: boolean;
  record: ToolPublicationRecord;
};

