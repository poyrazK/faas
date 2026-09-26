/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Target-owned HTTP request grant for one logical service caller. A path prefix matches itself and slash-delimited descendants; ambiguous request paths fail closed.
 */
export type ServiceCallScope = {
  methods: Array<string>;
  path_prefixes: Array<string>;
};

