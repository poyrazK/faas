/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * `false` opts this env or secret write out of the config guard
 * (ADR-1451): no guard opens, and a guard already watching the scope
 * closes as superseded. Omitted or `true` guards the change when the
 * scope has a live deployment.
 *
 */
export type ConfigGuard = boolean;
