/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CommitReceiptResponse } from './CommitReceiptResponse.js';
/**
 * Retained publication acceptance receipt with event metadata; excludes event data.
 */
export type CommitPublicationHistoryItem = (CommitReceiptResponse & {
  /**
   * Original committed event type.
   */
  type: string;
  /**
   * Selected schema version when supplied.
   */
  schema_version?: string;
});

