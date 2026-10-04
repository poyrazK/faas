/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exactly one action is required. Date must be a UTC midnight. A false marker action is retained as a no-op.
 */
export type ObjectLifecycleExpiration = {
  days?: number;
  date?: string;
  expired_object_delete_marker?: boolean;
};

