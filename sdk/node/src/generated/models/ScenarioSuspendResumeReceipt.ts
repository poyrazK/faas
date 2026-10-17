/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original-owner physical pause and resume proof, acknowledged by the scheduler after publishing the same VM with its captured runtime configuration.
 */
export type ScenarioSuspendResumeReceipt = {
  id: string;
  account_id: string;
  app_id: string;
  run_id: string;
  workload: string;
  deployment_id: string;
  instance_id: string;
  recorder_session_id: string;
  /**
   * Enrolled managed job; present only for managed_job checkpoints.
   */
  invocation_id?: string;
  /**
   * First dispatch held across the pause.
   */
  invocation_attempt?: number;
  /**
   * Independently completed simulation held by the original relay.
   */
  checkpoint_attempt_id?: string;
  state: 'waiting' | 'armed' | 'claimed' | 'fenced' | 'paused' | 'resuming' | 'resumed' | 'complete' | 'failed';
  method?: 'in_place';
  pause_ms: number;
  resume_ms: number;
};

