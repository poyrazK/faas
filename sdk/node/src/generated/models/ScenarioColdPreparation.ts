/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original-owner retirement and actual cold-boot receipts for one workload and deployment. Missing receipts never establish lifecycle parity.
 */
export type ScenarioColdPreparation = {
  id: string;
  account_id: string;
  app_id: string;
  run_id: string;
  workload: string;
  deployment_id: string;
  /**
   * Original serving VM when one existed at preparation.
   */
  instance_id?: string;
  /**
   * Original retired collector owner.
   */
  recorder_session_id?: string;
  state: 'armed' | 'claimed' | 'fenced' | 'retired' | 'ready' | 'booted' | 'failed';
  boot_instance_id?: string;
  boot_recorder_session_id?: string;
  wake_id?: string;
  method?: 'cold_boot';
  startup_ms: number;
};

