/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowForEachActionSpec } from './WorkflowForEachActionSpec.js';
/**
 * Sequential action over a JSON array from input or a direct dependency output.
 * Snapshots all items and resolved action inputs before dispatch. At most 128
 * items, 1 MiB source/prepared inputs and 1 MiB collected output. Parent names
 * permit at most 64 UTF-8 bytes. Stops on item failure; completed items survive
 * recovery. Output is an array of item outputs in input order; an empty list
 * succeeds with []. Parent consumes zero attempts; each item has its own ledger.
 *
 */
export type WorkflowForEachSpec = {
  /**
   * Array reference without delimiters, such as input.invoices or steps.lookup.output.items.
   */
  items: string;
  action: WorkflowForEachActionSpec;
};

