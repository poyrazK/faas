/** Portable child-run views; no credentials or application inputs. */
export type PipelineChildPhase = 'unstarted' | 'submission_unresolved' | 'running' | 'succeeded' | 'failed' | 'cancelled';
export type PipelineBatchStop = 'deadline' | 'child_failed' | 'parent_stopped' | null;
export interface PipelineChildPolicy {
  readonly timeoutMs: number;
  readonly concurrency?: number;
  readonly failurePolicy?: 'collect' | 'fail_fast';
  readonly onFailFast?: 'leave_running' | 'cancel_children';
  readonly onDeadline?: 'leave_running' | 'cancel_children';
  readonly onParentStopped?: 'leave_running' | 'cancel_children';
}
export interface PipelineChildView<O> {
  readonly id: string; readonly phase: PipelineChildPhase | 'skipped'; readonly run_id: string | null;
  readonly native_status?: string; readonly output?: O; readonly error?: 'child_failed';
  readonly cancel_requested: boolean;
  readonly waiting?: { readonly kind: 'approval' | 'input'; readonly gate_id: string; readonly name: string };
}
export interface PipelineBatchResult<O> {
  readonly identity: string; readonly parent_run_id: string; readonly batch: string;
  readonly stop_reason: PipelineBatchStop; readonly complete: boolean;
  readonly children: readonly PipelineChildView<O>[];
}
