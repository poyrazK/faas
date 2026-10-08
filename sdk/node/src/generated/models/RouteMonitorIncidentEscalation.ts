/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorEvidence } from './RouteMonitorEvidence.js';
import type { RouteMonitorIncidentEscalationSignal } from './RouteMonitorIncidentEscalationSignal.js';
/**
 * One stable route-monitor escalation transition. It preserves the newly violated signals and up to three fresh aggregate request/latency diagnostic entries; no customer identifiers are included.
 */
export type RouteMonitorIncidentEscalation = {
  /**
   * Same stable identifier as the corresponding routes.monitor.escalated outbox event.
   */
  transition_id: string;
  checked_at: string;
  previous_checked_at: string;
  newly_violated_routes: number;
  newly_violated_signals: number;
  signals: Array<RouteMonitorIncidentEscalationSignal>;
  evidence: Array<RouteMonitorEvidence>;
  /**
   * True when more signals changed than the three-entry evidence cap, or incident-size compaction removed evidence.
   */
  evidence_truncated: boolean;
};

