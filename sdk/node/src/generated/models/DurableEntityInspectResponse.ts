/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DurableEntityAlarmInspection } from './DurableEntityAlarmInspection.js';
import type { DurableEntityOutboxInspection } from './DurableEntityOutboxInspection.js';
import type { DurableEntityScope } from './DurableEntityScope.js';
export type DurableEntityInspectResponse = {
  /**
   * Opaque comparison value that changes on any manifest write; not ownership authority.
   */
  recovery_revision: string;
  entity: DurableEntityScope;
  /**
   * Business state uint64 version; zero means no transition has committed.
   */
  version: number;
  /**
   * Whether a business transition has committed; does not imply delivery.
   */
  state_committed: boolean;
  alarm: DurableEntityAlarmInspection;
  outbox: DurableEntityOutboxInspection;
};

