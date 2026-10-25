/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional loopback callback for new terminal init snapshots. A failure aborts capture. Enabling it disables warm snapshots; snapshot reuse skips the callback.
 */
export type BeforeCheckpointHook = {
  /**
   * Absolute path on the app's loopback HTTP listener. Called with POST and X-Faas-Before-Checkpoint: 1 immediately before pausing the VM.
   */
  path?: string;
  /**
   * Deadline for checkpoint response in milliseconds; 0 uses the 500 ms default.
   */
  timeout_ms?: number;
};

