/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A queue send, inbox message, delayed task or asynchronous invoke an enrolled app requested through the run-local platform API. It was never enqueued (ADR-20261021090000, ADR-20261022082721).
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
  /**
   * In a run with queue delivery, a send addressed to a member: delivered as a real invocation, unsupported, or over the delivery bound (ADR-20261023111825).
   */
  delivery?: 'delivered' | 'unsupported' | 'overflow';
  /**
   * A delivered send's invocation state when the run sealed.
   */
  delivery_state?: string;
};

