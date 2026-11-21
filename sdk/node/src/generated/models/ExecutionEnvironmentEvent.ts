/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A numbered lifecycle or build diagnostic record for a dependency environment.
 */
export type ExecutionEnvironmentEvent = {
  sequence: number;
  stage: string;
  code?: string;
  message: string;
  attempt: number;
  created_at: string;
};

