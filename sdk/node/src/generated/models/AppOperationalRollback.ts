/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Pending checked rollback progress with stable codes and target selection; free-form reasons and blocker diagnostics are omitted.
 */
export type AppOperationalRollback = {
  id: string;
  scope: string;
  status: string;
  code?: string;
  target_deployment_id: string;
  current_deployment_id: string;
  updated_at: string;
};

