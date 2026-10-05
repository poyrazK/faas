/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthClientErrorWindow } from './RouteHealthClientErrorWindow.js';
/**
 * Independent consecutive-window verdict for one watched status code. Different codes in different windows cannot confirm a sustained regression.
 */
export type RouteHealthClientErrorFinding = {
  status_code: 401 | 403 | 404 | 422 | 429;
  status: 'healthy' | 'regressed' | 'unknown';
  reason: string;
  windows: Array<RouteHealthClientErrorWindow>;
};

