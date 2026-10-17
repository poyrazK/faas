/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Capability and heartbeat verification against the exhaustive deployment-declared writer roster; not automatic discovery of unlisted processes or proof of metric data integrity.
 */
export type EventAcknowledgmentRecordingCoverage = {
  /**
   * All declared slots have one live compatible session and a continuous heartbeat watermark.
   */
  verified: boolean;
  /**
   * Bounded coverage diagnostic such as verified or missing_writer or inventory_unconfigured or warming_up.
   */
  reason: string;
  /**
   * Database clock used to assess writer leases in the metric read snapshot.
   */
  observed_at: string;
  /**
   * Conservative continuity boundary reset by observed membership changes or heartbeat gaps.
   */
  continuous_since?: string;
  /**
   * Least recent live writer heartbeat; no later minute is considered verified.
   */
  verified_through?: string;
  /**
   * Opaque hash of the common canonical declared inventory without exposing slots or sessions.
   */
  inventory_revision?: string;
  /**
   * Declared members when a usable inventory is available.
   */
  expected_writers: number;
  /**
   * Bounded live session count; 257 denotes evidence exceeding the scan limit.
   */
  active_writers: number;
  /**
   * Required deferred metric recording capability protocol.
   */
  capability_version: number;
  /**
   * Writer heartbeat lease horizon assessed using database time.
   */
  heartbeat_lease_seconds: number;
};

