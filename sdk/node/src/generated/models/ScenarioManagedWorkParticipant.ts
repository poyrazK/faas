/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One enrolled workload and its explicit resource capabilities. Async workers must execute at least one managed job.
 */
export type ScenarioManagedWorkParticipant = {
  workload: string;
  async?: boolean;
  /**
   * Access to the disposable PostgreSQL fixture. Task roles require seeded Task tables and a sole read_write primary.
   */
  postgres?: 'read_write' | 'task_runtime' | 'task_observer';
  objects?: 'read' | 'read_write';
};

