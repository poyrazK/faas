/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Binding name to explicit required queue evidence. Names match ^[a-z][a-z0-9-]{0,62}$. consumer_readiness requires one enabled push consumer with a healthy poll no older than thirty seconds; connectivity coverage remains partial.
 */
export type QueueReleaseRequirements = Record<string, 'consumer_readiness' | 'handler_completion'>;
