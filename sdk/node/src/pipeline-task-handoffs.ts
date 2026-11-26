/** Application-owned grants for one authenticated clarification. */
import { createHmac, timingSafeEqual } from "node:crypto";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import { jobSnapshot, jobValueHash } from "./job-runtime.js";
import type { PipelineQuery } from "./pipeline-state.js";
import type { PipelineTaskFormRequest } from "./pipeline-task-input.js";
import { pipelineToolSQL as sql } from "./pipeline-tool-protocol.js";
import { QueryToolError, type QueryOptions } from "./query-tools.js";

export { pipelineTaskHandoffSchema } from "./pipeline-tool-protocol.js";

export interface PipelineTaskHandoff {
  readonly handoff_id: string;
  readonly expires_at: string;
}
export interface PipelineTaskHandoffView extends PipelineTaskHandoff {
  readonly request_key: string;
  readonly form: PipelineTaskFormRequest;
}

export interface PipelineTaskHandoffSummary extends PipelineTaskHandoff {
  readonly question: string;
  readonly kind?: "approval";
}
export interface PipelineTaskHandoffPage {
  readonly items: readonly PipelineTaskHandoffSummary[];
  readonly next_cursor: string | null;
}
export interface PipelineTaskHandoffSnapshot {
  readonly items: readonly PipelineTaskHandoffSummary[];
  readonly truncated: boolean;
}

export interface PipelineTaskHandoffCandidate {
  readonly position: string;
  readonly grant: PipelineTaskHandoffRecord;
}

export interface PipelineTaskHandoffRecord {
  readonly id: string;
  readonly issuer_hash: string;
  readonly reviewer_hash: string;
  readonly task_id: string;
  readonly tool: string;
  readonly run_id: string;
  readonly gate_id: string;
  readonly binding_sha256: string;
  readonly expires_at: string;
}

export interface PipelineTaskHandoffStore {
  grant(namespace: string, record: PipelineTaskHandoffRecord, options: QueryOptions): Promise<PipelineTaskHandoffRecord>;
  load(namespace: string, id: string, reviewer: string, options: QueryOptions): Promise<PipelineTaskHandoffRecord | null>;
  scan?(namespace: string, reviewer: string, after: string, limit: number, options: QueryOptions): Promise<readonly PipelineTaskHandoffCandidate[]>;
  revoke(namespace: string, id: string, issuer: string, options: QueryOptions): Promise<boolean>;
}
export interface PipelineTaskHandoffSettings {
  readonly store: PipelineTaskHandoffStore;
  readonly scopes: readonly string[];
}

/** @internal Passed only by the handoff runtime to the gate decision store. */
export interface PipelineGateHandoff {
  readonly namespace: string;
  readonly id: string;
  readonly reviewer_hash: string;
  readonly run_id: string;
}

/** @internal Distinguish a user across clients while retaining the OAuth resource boundary. */
export function taskHandoffReviewer(secret: string, namespace: string, resource: string, subject: string): string {
  return createHmac("sha256", secret).update(jobValueHash(["gregale.pipeline-task-handoff.v1", namespace, resource, subject])).digest("hex");
}

/** @internal A cursor contains a scan position and a reviewer-bound MAC. */
export const handoffPosition = /^[0-9a-f]{32}$/;
export const handoffCursor = /^[0-9a-f]{32}\.[0-9a-f]{64}$/;
export const handoffPageLimit = 25;
export const handoffScanLimit = 64;

export function taskHandoffCursor(secret: string, reviewer: string, position: string): string {
  const mac = createHmac("sha256", secret).update(jobValueHash(["gregale.pipeline-task-handoff-list.v1", reviewer, position])).digest("hex");
  return position + "." + mac;
}

export function taskHandoffAfter(secret: string, reviewer: string, cursor: unknown): string {
  if (cursor === undefined) return "";
  if (typeof cursor !== "string" || !handoffCursor.test(cursor))
    throw new QueryToolError("invalid_input");
  const position = cursor.slice(0, 32);
  if (!timingSafeEqual(Buffer.from(cursor), Buffer.from(taskHandoffCursor(secret, reviewer, position))))
    throw new QueryToolError("invalid_input");
  return position;
}

/** @internal Authenticates an approval preview for this exact assignment and gate. */
export function taskHandoffApprovalKey(secret: string, row: PipelineTaskHandoffRecord, reviewDigest: string): string {
  const digest = jobValueHash(["gregale.pipeline-task-approval.v1", row.id, row.reviewer_hash, row.gate_id, row.binding_sha256, reviewDigest]);
  return createHmac("sha256", secret).update(digest).digest("hex");
}

/** @internal Validate persisted gate and reviewer bindings. */
export function parseTaskHandoff(value: unknown): PipelineTaskHandoffRecord {
  const row = value as PipelineTaskHandoffRecord;
  if (!row || Object.keys(row).sort().join() !== "binding_sha256,expires_at,gate_id,id,issuer_hash,reviewer_hash,run_id,task_id,tool" ||
      ![row.id, row.task_id, row.run_id].every(fileEventUUID) ||
      ![row.issuer_hash, row.reviewer_hash, row.gate_id, row.binding_sha256].every(fileEventHash) ||
      typeof row.tool !== "string" || !/^[a-z][a-z0-9_]{0,63}$/.test(row.tool) ||
      typeof row.expires_at !== "string" || !Number.isFinite(Date.parse(row.expires_at)))
    throw new QueryToolError("invalid_output");
  return jobSnapshot(row);
}

/** The handoff and pipeline gate stores use the same PostgreSQL primary. */
export function postgresPipelineTaskHandoffStore(query: PipelineQuery): PipelineTaskHandoffStore {
  async function read(statement: string, args: unknown[], options: QueryOptions) {
    const signal = options.signal ?? AbortSignal.timeout(options.timeoutMs);
    signal.throwIfAborted();
    const { rows } = await query(statement, args, signal);
    signal.throwIfAborted();
    return rows;
  }
  async function one(statement: string, args: unknown[], options: QueryOptions) {
    const rows = await read(statement, args, options);
    if (rows.length > 1) throw new QueryToolError("invalid_output");
    return rows[0];
  }
  return {
    async grant(namespace, grant, options) {
      const r = parseTaskHandoff(grant);
      return parseTaskHandoff(await one(sql.task_handoff_grant, [
        namespace, r.id, r.issuer_hash, r.reviewer_hash, r.task_id, r.tool, r.run_id, r.gate_id, r.binding_sha256, r.expires_at,
      ], options));
    },
    async load(namespace, id, reviewer, options) {
      const row = await one(sql.task_handoff_read, [namespace, id, reviewer], options);
      return row ? parseTaskHandoff(row) : null;
    },
    async scan(namespace, reviewer, after, limit, options) {
      const rows = await read(sql.task_handoff_scan, [namespace, reviewer, after, limit], options);
      return rows.map(({ position, ...grant }) => {
        if (typeof position !== "string" || !handoffPosition.test(position))
          throw new QueryToolError("invalid_output");
        return { position, grant: parseTaskHandoff(grant) };
      });
    },
    async revoke(namespace, id, issuer, options) {
      return (await one(sql.task_handoff_revoke, [namespace, id, issuer], options))?.id === id;
    },
  };
}
