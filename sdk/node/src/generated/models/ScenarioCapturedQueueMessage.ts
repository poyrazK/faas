/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A queue send, inbox message, delayed task or asynchronous invoke an enrolled app requested through the run-local platform API. It was never enqueued.
 */
export type ScenarioCapturedQueueMessage = {
  id: string;
  workload: string;
  kind: 'queue' | 'delayed_task' | 'async_invoke';
  /**
   * A member of the same run is named workload:<name>; any other app is app:<slug>.
   */
  target: string;
  queue_name?: string;
  /**
   * A delayed task's requested delay, or absolute when it gave scheduled_at.
   */
  schedule?: string;
  /**
   * The request's business content, bounded to 64 KiB.
   */
  message: any;
};

