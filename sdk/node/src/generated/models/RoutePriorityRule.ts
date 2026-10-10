/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Assigns a priority class to requests matching a method and path (ADR-947).
 */
export type RoutePriorityRule = {
  /**
   * Omitted matches every method.
   */
  method?: 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS';
  /**
   * Route template such as /users/{id}, or an edge-rule glob such as /exports*.
   */
  path: string;
  class: 'critical' | 'bulk';
};

