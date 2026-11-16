/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolRecoveryQualification } from './TaskToolRecoveryQualification.js';
/**
 * Owner-only handover or qualification verdict on the existing decision route. The authenticated native handler supplies the owner identity; agent inputs cannot supply it.
 */
export type TaskToolRecoveryRequest = {
  handover_id: string;
  operation_id: string;
  revision: number;
  qualification?: TaskToolRecoveryQualification;
  qualification_sha256?: string;
};

