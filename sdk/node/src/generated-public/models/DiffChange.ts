/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One diff row. Polymorphic values (Before / After) re-emitted
 * as JSON objects — the engine emits primitives, slices, or
 * structs depending on the field.
 *
 */
export type DiffChange = {
  /**
   * Human path: 'memory', 'concurrency', 'environment.<scope>.<key>', 'cron[<schedule> <path>]', 'edge_rule[<kind> <host><path>]'
   */
  field: string;
  kind: 'add' | 'remove' | 'modify';
  /**
   * Primitive value (int / string / bool / []string) — JSON-encoded. Omitted on Add.
   */
  before?: any;
  /**
   * Primitive value — JSON-encoded. Omitted on Remove.
   */
  after?: any;
};

