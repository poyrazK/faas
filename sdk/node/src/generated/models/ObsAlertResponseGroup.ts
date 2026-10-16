/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertResponseAggregate } from './ObsAlertResponseAggregate.js';
/**
 * One nonempty severity/component timing group with no actor identities or raw audit data.
 */
export type ObsAlertResponseGroup = {
  severity: 'critical' | 'error' | 'warning';
  component: string;
  metrics: ObsAlertResponseAggregate;
};

