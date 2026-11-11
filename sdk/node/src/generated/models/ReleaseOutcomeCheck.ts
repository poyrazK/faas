/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A recorded database check after a release command with an unknown outcome. Later release commands of the scope run again.
 */
export type ReleaseOutcomeCheck = {
  deployment_id: string;
  /**
   * The release command's task.
   */
  task_id: string;
  /**
   * How the release command ended, without an exit code.
   */
  task_status: 'failed' | 'timed_out' | 'cancelled';
  /**
   * The person or API key that recorded the check.
   */
  checked_by: string;
  /**
   * What the person recorded about the database.
   */
  note: string;
  checked_at: string;
};

