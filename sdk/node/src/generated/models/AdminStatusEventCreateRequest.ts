/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AdminStatusIncidentCreateRequest } from './AdminStatusIncidentCreateRequest.js';
import type { AdminStatusMaintenanceCreateRequest } from './AdminStatusMaintenanceCreateRequest.js';
/**
 * Operator request to publish an incident or schedule maintenance, discriminated by kind.
 */
export type AdminStatusEventCreateRequest = (AdminStatusIncidentCreateRequest | AdminStatusMaintenanceCreateRequest);

