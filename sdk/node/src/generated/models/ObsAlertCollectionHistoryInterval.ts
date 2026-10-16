/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Half-open coalesced interval of fresh outcome evidence or unknown observation coverage.
 */
export type ObsAlertCollectionHistoryInterval = {
  from: string;
  until: string;
  state: 'success' | 'failure' | 'unknown';
  /**
   * Empty on successful observations; failures retain only closed codes; unknown intervals have no_recent_outcome without an inferred cause.
   */
  reason: '' | 'collection_failed' | 'source_read_failed' | 'snapshot_rejected' | 'timeout' | 'persistence_failed' | 'no_recent_outcome';
};

