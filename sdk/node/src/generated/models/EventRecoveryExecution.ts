/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only observation of the exact admitted replay or workflow resume. Invocation mode prefers saved confirmed terminal results, retained with the recovery job. Workflow mode prefers saved terminal results for the admitted resume, then live retained run and resume evidence. Missing evidence without a saved result is unknown. Native failed/dead runs both report failed. Pending and running workflow states report queued and running; awaiting_event also reports running. Without a saved result, later native resumes report superseded with no completion time. Saved native results survive workflow-history pruning for the recovery job retention period; the first confirmed terminal result wins across later resumes. Omitted for routing recovery and items not admitted.
 */
export type EventRecoveryExecution = {
  /**
   * When terminal evidence was saved; present only for recovery_result.
   */
  recorded_at?: string;
  /**
   * Original source of a saved terminal result; present only for recovery_result.
   */
  evidence_source?: 'invocation' | 'attempt_history' | 'workflow_run';
  observed_at: string;
  state: 'queued' | 'running' | 'retrying' | 'succeeded' | 'failed' | 'dead_lettered' | 'expired' | 'cancelled' | 'superseded' | 'unknown';
  source: 'invocation' | 'attempt_history' | 'recovery_result' | 'workflow_run' | 'unavailable';
  /**
   * Recorded dispatch attempt count for the tracked invocation generation; zero for workflow-run observations.
   */
  attempts: number;
  /**
   * Recorded terminal completion time when available.
   */
  completed_at?: string;
};

