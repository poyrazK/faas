/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One frame of the wake timeline (issue #517 / PR-C /
 * ADR-064). The shape mirrors the typed event payloads
 * the producers write — see `pkg/events/wake.go`. The
 * canonical `wake.*` vocabulary is documented in
 * `docs/adr/064-wake-timeline-canonical-vocabulary.md`
 * (including `wake.restore_breakdown`, which exposes the
 * vmmd snapshot-restore phases in integer milliseconds;
 * `wake.cold_boot_breakdown`, which attributes pre-guest artifact
 * resolution and Firecracker startup phases, including artifact source,
 * duration, and byte size;
 * `wake.cold_boot_cpu`, which records the temporary startup CPU
 * allowance and configured quota restored before routing; and
 * build/deploy/boot failure kinds).
 *
 */
export type WakeTimelineEvent = {
  /**
   * RFC 3339 UTC. Oldest-first (forward narrative).
   */
  at: string;
  /**
   * Canonical `wake.*` kind. See ADR-064.
   */
  kind: string;
  /**
   * Daemon that wrote the row (`schedd` / `vmmd` / `gatewayd` / `egress` / `builderd` / `apid`).
   */
  actor: string;
  /**
   * Producer-supplied payload (json object).
   */
  data: Record<string, any>;
};

