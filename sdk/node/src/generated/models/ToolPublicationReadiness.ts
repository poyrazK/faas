/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolPublicationReadinessRole } from './ToolPublicationReadinessRole.js';
/**
 * Reviewed default-environment role expectations without credential sources or monitors.
 */
export type ToolPublicationReadiness = {
  policy_revision: string;
  endpoint: string;
  max_age_seconds: number;
  roles: Array<ToolPublicationReadinessRole>;
};

