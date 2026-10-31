/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A cron expression lifted from a workload (k8s CronJob, render.yaml, serverless.yml).
 */
export type PlanCron = {
  workload_name: string;
  schedule: string;
  path: string;
  enabled: boolean;
};

