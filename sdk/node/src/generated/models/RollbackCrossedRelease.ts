/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A release command that ran after a rollback target last served (ADR-1448). A rollback does not undo it.
 */
export type RollbackCrossedRelease = {
  deployment_id: string;
  /**
   * The deployment's vN revision.
   */
  revision?: number;
  /**
   * The release task's status. A failed release may still have committed changes.
   */
  status: string;
  started_at: string;
};

