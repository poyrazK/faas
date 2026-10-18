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
  postgres?: 'read_write';
  objects?: 'read' | 'read_write';
};

