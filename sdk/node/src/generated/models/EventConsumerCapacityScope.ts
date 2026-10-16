/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current usage and ceiling together with this subscription's effective recorded waits for this scope. Advice is informational and never initiates recovery or changes controls.
 */
export type EventConsumerCapacityScope = {
  scope: 'consumer' | 'app' | 'account';
  /**
   * Trusted pending plus dispatching delivery slots, including replay descendants; excludes untagged legacy invocations.
   */
  live_deliveries: number;
  /**
   * Current plan ceiling, clamped by a configured subscription ceiling for consumer scope.
   */
  effective_limit: number;
  /**
   * Live usage is greater than or equal to the effective ceiling.
   */
  at_limit: boolean;
  /**
   * This subscription's retained pending application recipients whose effective reason is capacity at this scope. Precedence follows the existing backlog pause, circuit, ordering and routing-state rules.
   */
  waiting_recipients: number;
  /**
   * Acceptance timestamp of the oldest such recipient; absent when no recipients wait at this scope.
   */
  oldest_waiting_accepted_at?: string;
  /**
   * Age since acceptance at observed_at, not continuous capacity-wait duration; zero when none wait.
   */
  oldest_waiting_age_seconds: number;
  /**
   * Suggested read-only investigation or operator review based on current scope pressure.
   */
  advice: string;
};

