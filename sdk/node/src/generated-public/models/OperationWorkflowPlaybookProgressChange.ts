/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookProgressItem } from './OperationWorkflowPlaybookProgressItem.js';
/**
 * OperationWorkflowPlaybookProgressChange. Durable advisory checklist coordination, independent of application business success.
 */
export type OperationWorkflowPlaybookProgressChange = {
  /**
   * OperationWorkflowPlaybookProgressChange.outcome_code. Present for outcome follow-up step reviews; item_index is local to that list.
   */
  outcome_code?: string;
  /**
   * OperationWorkflowPlaybookProgressChange.revision. Committed revision after this edit.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookProgressChange.item_index. Edited frozen checklist position.
   */
  item_index: number;
  /**
   * OperationWorkflowPlaybookProgressChange.item. Full reviewed item snapshot at this edit.
   */
  item: OperationWorkflowPlaybookProgressItem;
  /**
   * OperationWorkflowPlaybookProgressChange.at. Server commit preparation clock.
   */
  at: string;
  /**
   * OperationWorkflowPlaybookProgressChange.actor. Server-derived reviewer identity.
   */
  actor: string;
};

