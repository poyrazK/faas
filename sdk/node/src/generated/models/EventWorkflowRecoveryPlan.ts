/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventWorkflowRecoveryTarget } from './EventWorkflowRecoveryTarget.js';
/**
 * Explicit reviewed workflow acceptance recipients. Routing mode only, requires request_id and excludes parent and backlog filters. Creation freezes the exact selection. Before each admission the worker atomically compares published version, replay count and routing generation; changes are skipped. Repeated request_id returns the retained job, even after completion; a different normalized plan or rate conflicts. Preview and preflight are advisory; workflow fence eligibility is unknown in preflight. Queued means routing recovery was admitted, not workflow completion.
 */
export type EventWorkflowRecoveryPlan = {
  name: string;
  published_version: number;
  source: string;
  events: Array<EventWorkflowRecoveryTarget>;
};

