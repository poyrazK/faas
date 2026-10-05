/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A step attempt within a durable workflow run.
 */
export type WorkflowStepResponse = {
  /**
   * Attempt number at the latest resume; attempts since this number consume the current retry budget.
   */
  retry_base?: number;
  /**
   * Parent step name for a persisted iteration item.
   */
  for_each_parent?: string;
  /**
   * Stable zero-based index within the snapshotted list.
   */
  for_each_index?: number;
  /**
   * Snapshotted item count on an initialized parent; zero is an empty batch.
   */
  for_each_count?: number;
  step_name: string;
  status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead' | 'skipped';
  attempt: number;
  input?: any;
  output?: any;
  /**
   * Persisted guard decision; absent when the guard has not run or a dependency was skipped.
   */
  when_matched?: boolean;
  when_evaluated_at?: string;
  /**
   * Why a pending step was skipped; contains no referenced customer values.
   */
  skip_reason?: 'when_false' | 'dependency_skipped' | 'dependency_failed' | 'route_not_taken';
  started_at?: string | null;
  next_check_at?: string | null;
  next_retry_at?: string | null;
  finished_at?: string | null;
  error?: string | null;
  created_at: string;
};

