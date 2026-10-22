/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CommitPublicationHistoryItem } from './CommitPublicationHistoryItem.js';
/**
 * Newest-first retained acceptance history, independent of subscriber evidence retention.
 */
export type CommitPublicationHistoryResponse = {
  items: Array<CommitPublicationHistoryItem>;
  /**
   * Opaque source-scoped continuation cursor; absent on the last observed page.
   */
  next_before?: string;
};

