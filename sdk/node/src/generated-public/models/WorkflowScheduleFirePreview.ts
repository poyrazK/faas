/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One canonical fire time with its offset-bearing local representation.
 */
export type WorkflowScheduleFirePreview = {
  /**
   * UTC instant used by the scheduler.
   */
  scheduled_for: string;
  /**
   * Local wall time including the UTC offset.
   */
  local_time: string;
  /**
   * Present when a spring gap moves the configured wall time to the first valid minute.
   */
  dst_adjustment?: 'shifted_forward';
};

