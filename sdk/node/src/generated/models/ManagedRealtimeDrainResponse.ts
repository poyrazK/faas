/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedRealtimeDrainResult } from './ManagedRealtimeDrainResult.js';
/**
 * Bounded, auditable result of a realtime connection drain.
 */
export type ManagedRealtimeDrainResponse = {
  results: Array<ManagedRealtimeDrainResult>;
  /**
   * Number selected after applying the limit.
   */
  matched: number;
  closed: number;
  /**
   * Selected connections that disappeared before close.
   */
  gone: number;
  failed: number;
  limit: number;
  /**
   * True when more eligible connections existed than the limit.
   */
  truncated: boolean;
  dry_run: boolean;
  /**
   * Indicates that the inventory did not cover every active realtime node.
   */
  partial: boolean;
  nodes_queried: number;
  nodes_unavailable: number;
};

