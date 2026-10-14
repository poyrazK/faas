/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * On-demand capture request (ADR-1155). Kinds defaults to [cpu]; duration_seconds to 10. Stacks is an instant snapshot of where every goroutine or thread is waiting, taken when the capture starts.
 */
export type CreateProfileCaptureRequest = {
  kinds?: Array<'cpu' | 'heap' | 'stacks'>;
  duration_seconds?: number;
  /**
   * Running instance to profile; empty selects the earliest-started RUNNING instance.
   */
  instance_id?: string;
};

