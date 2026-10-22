/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A workflow, realtime or Run call an enrolled app requested through the run-local platform API. It was never performed (ADR-20261021150000, ADR-20261021180000, ADR-20261022170348).
 */
export type ScenarioCapturedPlatformCall = {
  id: string;
  workload: string;
  kind: 'workflow_run' | 'workflow_event' | 'realtime_publish' | 'realtime_signal' | 'execution' | 'execution_cancel';
  /**
   * What the call addressed, without platform IDs the run created. A Run start addresses execution:runs.
   */
  target: string;
  /**
   * The workflow, event or channel name, or a Run's runtime.
   */
  name: string;
  /**
   * The call's compared content, bounded to 64 KiB.
   */
  message: any;
};

