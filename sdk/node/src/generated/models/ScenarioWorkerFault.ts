/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Platform-owned checkpoint, collector and VM termination identity. Complete evidence requires a terminated receipt and independently journalled recovery on a different VM.
 */
export type ScenarioWorkerFault = {
  id: string;
  account_id: string;
  app_id: string;
  run_id: string;
  workload: string;
  deployment_id: string;
  instance_id: string;
  invocation_id: string;
  checkpoint_attempt_id: string;
  recorder_session_id: string;
  state: 'armed' | 'ready' | 'claimed' | 'fenced' | 'terminated' | 'failed';
  recovery_instance_id?: string;
};

