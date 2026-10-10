/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Whether waking this caller also starts restores for its declared service bindings (ADR-950). `declared` restores up to 8 resolvable, authorized dependencies in parallel with the caller; `off` restores each dependency only when it is called.
 */
export type ServiceWakeAhead = 'off' | 'declared';
