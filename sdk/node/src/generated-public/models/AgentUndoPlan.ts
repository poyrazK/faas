/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AgentUndoItem } from './AgentUndoItem.js';
/**
 * An agent's changes, newest first, and what undoing them will do.
 */
export type AgentUndoPlan = {
  /**
   * The oldest change time the plan covers.
   */
  since: string;
  items: Array<AgentUndoItem>;
  /**
   * How many changes applying the plan will undo.
   */
  undoable: number;
  /**
   * Binds an apply to this plan.
   */
  plan_digest: string;
};

