/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Acknowledgement that the versioned webhook capture policy was committed before source deployment.
 */
export type RegisterScenarioTestResponse = {
  /**
   * Acknowledges the exact suspend/resume plan and workload inventory before deployment.
   */
  suspend_resume_sha256?: string;
  /**
   * Acknowledges the exact cold-start budget and workload inventory before deployment.
   */
  cold_start_sha256?: string;
  /**
   * Acknowledges the exact durable participant plan before deployment.
   */
  managed_work_sha256?: string;
  /**
   * Echoes supported contained command execution. Each command also requires a matching guest receipt and owner retirement before success.
   */
  command_version?: 1;
  /**
   * Required when simulation is enrolled. Acknowledges durable recorder support before source deployment.
   */
  recorder_version?: 1;
  /**
   * Echoes the canonical fixture plan identity after durable enrollment.
   */
  simulation_sha256?: string;
  /**
   * Echoes the durable network containment enrollment. Each runtime must also acknowledge the exact run before publication.
   */
  network_mode?: 'contained';
  /**
   * Present when the requested sealed action-evidence protocol is supported.
   */
  action_evidence_version?: 1 | 2;
  /**
   * Acknowledges same-run queue delivery.
   */
  queue_delivery_version?: 1;
  webhook_mode: 'capture';
  policy_version: 1;
};

