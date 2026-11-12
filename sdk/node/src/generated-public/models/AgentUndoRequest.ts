/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Chooses an agent's changes to undo: those since a time (default the last 24 hours), or the activity entries named.
 */
export type AgentUndoRequest = {
  /**
   * Undo changes made at or after this time; at most 7 days ago.
   */
  since?: string;
  /**
   * Activity entry ids to undo; when given, only they are undone.
   */
  entries?: Array<string>;
  /**
   * Also undo changes someone changed again since the agent did.
   */
  force?: boolean;
  /**
   * On apply, the plan_digest of the reviewed plan.
   */
  plan_digest?: string;
};

