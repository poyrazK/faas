/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowForEachActionSpec } from './WorkflowForEachActionSpec.js';
/**
 * Bounded-concurrency action over a JSON array from input or a direct dependency
 * output. Snapshots all items and resolved action inputs before dispatch. At most
 * 128 items, 1 MiB source/prepared inputs and 1 MiB collected output. Parent names
 * permit at most 64 UTF-8 bytes. Omitted or zero max_parallel means one active
 * item; values up to 16 limit active items per batch. Items are admitted in
 * input order within a bounded window, and collected output always follows input
 * order. By default no new items start after a terminal item failure; already
 * active items finish and the parent fails. `on_item_failure: continue` attempts
 * later items and still marks the parent unsuccessful if any item failed.
 * Completed items survive recovery. With the default stop policy, output retains
 * the completed prefix. With continue, output includes every input position and
 * null for guarded or unsuccessful items. An empty list succeeds with []. Parent
 * consumes zero attempts; each item has its own ledger.
 *
 */
export type WorkflowForEachSpec = {
  /**
   * Array reference without delimiters, such as input.invoices or steps.lookup.output.items.
   */
  items: string;
  action: WorkflowForEachActionSpec;
  /**
   * Maximum active items for this batch. Omit or set 0 for sequential execution.
   */
  max_parallel?: number;
  /**
   * Continue after failed or dead items and mark the parent unsuccessful after all items are attempted. Omit to stop at the first failure.
   */
  on_item_failure?: 'continue';
};

