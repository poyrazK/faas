/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable dispatch ownership and terminal outcome, retained across retries. Unknown outcomes cannot establish completion.
 */
export type ScenarioManagedWorkAttempt = {
  number: number;
  instance_id: string;
  deployment_id: string;
  outcome: string;
};

