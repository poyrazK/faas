/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Project environment slug (ADR-20261104120000). Omitted or production is production. A stage's stored workflows are its own; it runs its manual and schedule workflows on its own deployment. A workflow is reached only through its own environment.
 */
export type StoredWorkflowEnvironment = string;
