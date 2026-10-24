/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Caller-side authorization policy for internal service requests. `account` preserves same-account reachability; `declared` permits only targets present in the caller's service bindings; `project` also permits every service in the caller's own project (ADR-20261022111119).
 */
export type ServiceBindingPolicy = 'account' | 'declared' | 'project';
