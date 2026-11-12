/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AgentUndoItem } from './AgentUndoItem.js';
/**
 * What an applied undo did to each change, newest first.
 */
export type AgentUndoResult = {
  items: Array<AgentUndoItem>;
  /**
   * Changes undone.
   */
  undone: number;
  /**
   * Changes whose undo was refused.
   */
  failed: number;
};

