/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Manual start, a five-field recurring schedule, or an internal event start. Event triggers use source/event_type patterns and a JSON content filter; matching runs receive the full CloudEvents envelope as input. Event recipients and workflow definitions are captured when an event is accepted. Scheduled starts skip missed minutes and default to skipping overlapping runs. Scheduling is active only on the live default deployment and requires the workflow runtime. New deployments arm schedules before their next eligible minute. An app owner may mark a schedule tenant_configurable to let each linked customer manage only its own cadence, timezone, overlap behavior, and enabled state.
 */
export type WorkflowTriggerSpec = {
  type: 'manual' | 'schedule' | 'event';
  /**
   * Five-field cron expression, required for schedule triggers.
   */
  schedule?: string;
  /**
   * IANA timezone for schedule triggers; defaults to UTC.
   */
  timezone?: string;
  /**
   * Fixed JSON input for scheduled runs, bounded by the workflow run-input limit.
   */
  input?: any;
  /**
   * Skip a minute while any run of this workflow is active, or allow overlap subject to the app run quota. Defaults to skip.
   */
  overlap?: 'skip' | 'allow';
  /**
   * Whether the automatic trigger is enabled; defaults to true. Already accepted events and existing runs continue after disabling.
   */
  enabled?: boolean;
  /**
   * For schedule triggers, allow each linked platform tenant to manage its own schedule, timezone, overlap behavior, and enabled state. Workflow input and definition remain app-owned.
   */
  tenant_configurable?: boolean;
  /**
   * Required for event triggers; exact source or edge wildcard pattern.
   */
  source?: string;
  /**
   * Required for event triggers; exact event type or edge wildcard pattern.
   */
  event_type?: string;
  /**
   * Optional JSON predicate evaluated against the CloudEvents envelope. Event triggers reject schedule, timezone, input, and overlap options.
   */
  filter?: Record<string, any>;
};

