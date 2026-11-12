/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One agent change in an undo plan or result.
 */
export type AgentUndoItem = {
  /**
   * The activity entry of the change.
   */
  activity_id: string;
  /**
   * The HTTP method of the agent's request.
   */
  method: string;
  /**
   * The path of the agent's request.
   */
  path: string;
  /**
   * What kind of change it was.
   */
  kind: 'settings' | 'deploy' | 'create' | 'delete' | 'other';
  /**
   * What the agent changed.
   */
  summary: string;
  /**
   * When the agent made the change.
   */
  created_at: string;
  /**
   * What undoing it will do, or did.
   */
  status: 'undoable' | 'not_undoable' | 'changed_since' | 'undone';
  /**
   * Why it cannot be undone, or why it was left.
   */
  reason?: string;
  /**
   * On apply, what happened to it.
   */
  result?: 'undone' | 'changed_since' | 'failed' | 'skipped';
  /**
   * On a failed apply, the code of the refusal.
   */
  error_code?: string;
  /**
   * On a failed apply, why it was refused.
   */
  error?: string;
};

