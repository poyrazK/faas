/** Browser-safe, authorized snapshots for one business record. Realtime frames are hints. */
import type { WorkInboxEntry } from "./work-inbox.js";

export interface WorkInboxWatchTarget {
  readonly resource_id: string;
  readonly business_ref: string;
}
export interface LiveWorkInboxSnapshot extends WorkInboxWatchTarget {
  readonly version: 1;
  readonly items: readonly WorkInboxEntry[];
  readonly truncated: boolean;
}
export interface WorkInboxInvalidation {
  readonly version: 1;
  readonly kind: "work_inbox_changed";
  readonly resource_id: string;
  readonly change_id: string;
}
export type LiveWorkInboxCode =
  | "unauthorized"
  | "invalid_snapshot"
  | "snapshot_failed"
  | "subscription_failed"
  | "deadline"
  | "cancelled"
  | "snapshot_limit";
export class LiveWorkInboxError extends Error {
  constructor(readonly code: LiveWorkInboxCode) {
    super("Live work inbox: " + code);
    this.name = "LiveWorkInboxError";
  }
}
export interface LiveWorkInboxSink {
  ready(): void;
  changed(event: unknown): void;
  disconnected(): void;
  failed(code?: "unauthorized" | "subscription_failed"): void;
}
export type LiveWorkInboxSource = (
  sink: LiveWorkInboxSink,
  signal: AbortSignal,
) => () => void;
export interface LiveWorkInboxOptions {
  readonly target: WorkInboxWatchTarget;
  readonly source: LiveWorkInboxSource;
  readonly loadSnapshot: (options: {
    timeoutMs: number;
    signal: AbortSignal;
  }) => Promise<unknown>;
  readonly timeoutMs: number;
  readonly signal?: AbortSignal;
  /** Fallback reads while subscribed. Default 15 seconds. */
  readonly refreshIntervalMs?: number;
  readonly maxSnapshots?: number;
}
const id = (value: unknown): value is string =>
  typeof value === "string" && /^[A-Za-z0-9_.:-]{1,128}$/.test(value);
const hash = (value: unknown): value is string =>
  typeof value === "string" && /^[0-9a-f]{64}$/.test(value);
const uuid = (value: unknown): value is string =>
  typeof value === "string" &&
  /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/.test(value) &&
  value !== "00000000-0000-0000-0000-000000000000";
const isObject = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === "object" && !Array.isArray(v);
const equalKeys = (v: Record<string, unknown>, expected: string) =>
  Object.keys(v).sort().join() === expected;
const invalidSnapshot = () => new LiveWorkInboxError("invalid_snapshot");
const pipelineStates = new Set([
  "start_unknown",
  "running",
  "awaiting_approval",
  "awaiting_input",
  "succeeded",
  "failed",
  "cancelled",
]);
const actionStates = new Set([
  "prepared",
  "unknown",
  "pending",
  "succeeded",
  "failed",
  "needs_review",
  "cancelled",
]);
const steps = new Set([
  "reconcile_start",
  "open_review",
  "inspect",
  "read_result",
  "reconcile_action",
  "review_action",
  "none",
]);
export function workInboxWatchTarget(
  target: WorkInboxWatchTarget,
): WorkInboxWatchTarget {
  if (!id(target?.resource_id) || !id(target?.business_ref))
    throw new TypeError("Invalid work inbox watch target");
  return Object.freeze({
    resource_id: target.resource_id,
    business_ref: target.business_ref,
  });
}

export function workInboxInvalidation(
  resourceId: string,
  changeId: string,
): WorkInboxInvalidation {
  if (!id(resourceId) || !id(changeId))
    throw new TypeError("Invalid work inbox invalidation");
  return Object.freeze({
    version: 1,
    kind: "work_inbox_changed",
    resource_id: resourceId,
    change_id: changeId,
  });
}
function changeId(
  event: unknown,
  target: WorkInboxWatchTarget,
): string | undefined {
  if (
    isObject(event) &&
    equalKeys(event, "change_id,kind,resource_id,version") &&
    event.version === 1 &&
    event.kind === "work_inbox_changed" &&
    event.resource_id === target.resource_id &&
    id(event.change_id)
  )
    return event.change_id;
}
function reviewLink(
  value: unknown,
  runId: string,
  gateId: string,
): value is string {
  if (typeof value !== "string" || value.length > 2048) return false;
  try {
    const url = new URL(value);
    return (
      !url.username &&
      !url.password &&
      !url.hash &&
      (url.protocol === "https:" ||
        (url.protocol === "http:" &&
          ["127.0.0.1", "localhost", "[::1]"].includes(url.hostname))) &&
      [...url.searchParams.keys()].sort().join() === "gate,run" &&
      url.searchParams.get("gate") === gateId &&
      url.searchParams.get("run") === runId
    );
  } catch {
    return false;
  }
}
/** @internal Validate a review before passing its link to a caller. */
export function parseWorkInboxReview(
  review: unknown,
  state: "awaiting_approval" | "awaiting_input",
  runId: unknown,
): NonNullable<WorkInboxEntry["review"]> {
  const reviewKind = state === "awaiting_approval" ? "approval" : "input";
  if (
    !isObject(review) ||
    !equalKeys(review, "expires_at,id,kind,review_url") ||
    review.kind !== reviewKind ||
    !hash(review.id) ||
    typeof review.expires_at !== "string" ||
    review.expires_at.length > 64 ||
    !Number.isFinite(Date.parse(review.expires_at)) ||
    !uuid(runId) ||
    !reviewLink(review.review_url, runId, review.id)
  )
    throw invalidSnapshot();
  return Object.freeze({
    kind: reviewKind,
    id: review.id,
    expires_at: review.expires_at,
    review_url: review.review_url,
  });
}
function readItem(
  value: unknown,
  target: WorkInboxWatchTarget,
): WorkInboxEntry {
  if (!isObject(value)) throw invalidSnapshot();
  const {
    kind,
    name,
    state,
    next_step,
    request_id,
    business_ref,
    run_id,
    action_id,
    review,
  } = value;
  const pipeline = kind === "pipeline",
    waiting = state === "awaiting_approval" || state === "awaiting_input";
  if (
    (!pipeline && kind !== "action") ||
    typeof name !== "string" ||
    !/^[a-z][a-z0-9_]{0,63}$/.test(name) ||
    typeof state !== "string" ||
    !(pipeline ? pipelineStates : actionStates).has(state) ||
    typeof next_step !== "string" ||
    !steps.has(next_step) ||
    !uuid(request_id) ||
    business_ref !== target.business_ref ||
    !(pipeline
      ? action_id === undefined &&
        (state === "start_unknown" ? run_id === undefined : uuid(run_id))
      : hash(action_id) && run_id === undefined) ||
    waiting !== (review !== undefined) ||
    Object.keys(value).some(
      (k) =>
        ![
          "kind",
          "name",
          "state",
          "next_step",
          "request_id",
          "business_ref",
          "run_id",
          "action_id",
          "review",
        ].includes(k),
    )
  )
    throw invalidSnapshot();
  const currentReview = waiting
    ? parseWorkInboxReview(review, state, run_id)
    : undefined;
  return Object.freeze({
    kind,
    name,
    state: state as WorkInboxEntry["state"],
    next_step: next_step as WorkInboxEntry["next_step"],
    request_id,
    business_ref,
    ...(uuid(run_id) ? { run_id } : {}),
    ...(hash(action_id) ? { action_id } : {}),
    ...(currentReview ? { review: currentReview } : {}),
  });
}
export function parseLiveWorkInboxSnapshot(
  raw: unknown,
  expected: WorkInboxWatchTarget,
): LiveWorkInboxSnapshot {
  const target = workInboxWatchTarget(expected);
  if (
    !isObject(raw) ||
    !equalKeys(raw, "business_ref,items,resource_id,truncated,version") ||
    raw.version !== 1 ||
    raw.resource_id !== target.resource_id ||
    raw.business_ref !== target.business_ref ||
    typeof raw.truncated !== "boolean" ||
    !Array.isArray(raw.items) ||
    raw.items.length > 100
  )
    throw invalidSnapshot();
  const items = raw.items.map((item) => readItem(item, target));
  const keys = items.map((item) =>
    [item.kind, item.name, item.action_id ?? item.request_id].join(":"),
  );
  if (new Set(keys).size !== keys.length) throw invalidSnapshot();
  return Object.freeze({
    version: 1,
    ...target,
    items: Object.freeze(items),
    truncated: raw.truncated,
  });
}

function duration(value: number): boolean {
  return Number.isSafeInteger(value) && value > 0 && value <= 2147483647;
}
type SnapshotLoader<T> = (options: { timeoutMs: number; signal: AbortSignal }) => Promise<T>;
async function load<T>(
  loadSnapshot: SnapshotLoader<T>,
  signal: AbortSignal,
  timeoutMs: number,
): Promise<T> {
  let cancel!: () => void;
  const interrupted = new Promise<never>((_resolve, reject) => {
    cancel = () => reject(new LiveWorkInboxError("cancelled"));
  });
  signal.addEventListener("abort", cancel, { once: true });
  try {
    signal.throwIfAborted();
    return await Promise.race([
      Promise.resolve().then(() => {
        signal.throwIfAborted();
        return loadSnapshot({ timeoutMs, signal });
      }),
      interrupted,
    ]);
  } finally {
    signal.removeEventListener("abort", cancel);
  }
}

/** A lazy iterator. Hold one per authenticated principal and target. */
export function watchWorkInbox(
  input: LiveWorkInboxOptions,
): AsyncIterableIterator<LiveWorkInboxSnapshot> {
  return watchInboxSnapshots(input, parseLiveWorkInboxSnapshot);
}

/** @internal Share the reconnect and authorization lifecycle across inbox shapes. */
export function watchInboxSnapshots<Raw, T>(
  input: Omit<LiveWorkInboxOptions, "loadSnapshot"> & { readonly loadSnapshot: SnapshotLoader<Raw> },
  parse: (raw: Raw, target: WorkInboxWatchTarget) => T,
  retryRead?: (error: unknown) => boolean,
): AsyncIterableIterator<T> {
  const options = { ...input },
    target = workInboxWatchTarget(options.target);
  const interval = options.refreshIntervalMs ?? 15000,
    limit = options.maxSnapshots ?? 1000;
  if (
    !duration(options.timeoutMs) ||
    !duration(interval) ||
    !Number.isSafeInteger(limit) ||
    limit < 1 ||
    limit > 10000 ||
    typeof options.source !== "function" ||
    typeof options.loadSnapshot !== "function"
  )
    throw new TypeError("Invalid live work inbox options");
  type Stop = LiveWorkInboxCode | "closed";
  let close: ((reason: Stop) => void) | undefined;
  async function* stream(): AsyncGenerator<T> {
    const lifecycle = new AbortController(),
      deadline = performance.now() + options.timeoutMs;
    const seen = new Set<string>();
    let release: (() => void) | undefined,
      resume: (() => void) | undefined,
      reader: AbortController | undefined;
    let terminal: Stop | undefined,
      prior: string | undefined,
      period: ReturnType<typeof setInterval> | undefined;
    let connected = false,
      dirty = false,
      epoch = 0,
      count = 0;
    const wake = () => {
      resume?.();
      resume = undefined;
    };
    const invalidate = () => {
      if (!terminal && connected) {
        dirty = true;
        wake();
      }
    };
    const abort = () => stop("cancelled");
    const timer = setTimeout(() => stop("deadline"), options.timeoutMs);
    function stop(reason: Stop) {
      if (terminal) return;
      terminal = reason;
      clearTimeout(timer);
      clearInterval(period);
      options.signal?.removeEventListener("abort", abort);
      lifecycle.abort();
      reader?.abort();
      try {
        release?.();
      } catch {
        /* terminal state is authoritative */
      }
      wake();
    }
    close = stop;
    try {
      if (options.signal?.aborted) stop("cancelled");
      else {
        options.signal?.addEventListener("abort", abort, { once: true });
        try {
          release = options.source(
            {
              ready() {
                if (terminal) return;
                if (!connected) {
                  connected = true;
                  period = setInterval(invalidate, interval);
                }
                invalidate();
              },
              changed(event) {
                if (terminal || !connected) return;
                const key = changeId(event, target);
                if (!key || seen.has(key)) return;
                if (seen.size >= 256) seen.delete(seen.values().next().value!);
                seen.add(key);
                invalidate();
              },
              disconnected() {
                if (terminal || !connected) return;
                connected = dirty = false;
                clearInterval(period);
                prior = undefined;
                epoch++;
                reader?.abort();
              },
              failed(code) {
                stop(code === "unauthorized" ? code : "subscription_failed");
              },
            },
            lifecycle.signal,
          );
          if (terminal) {
            try {
              release?.();
            } catch {
              /* already stopped */
            }
          }
        } catch {
          stop("subscription_failed");
        }
      }
      while (!terminal) {
        if (!connected || !dirty) {
          await new Promise<void>((resolve) => {
            resume = resolve;
          });
          continue;
        }
        dirty = false;
        if (++count > limit) {
          stop("snapshot_limit");
          break;
        }
        const left = deadline - performance.now();
        if (left <= 0) {
          stop("deadline");
          break;
        }
        const generation = epoch;
        reader = new AbortController();
        try {
          const raw = await load(
            options.loadSnapshot,
            reader.signal,
            Math.max(1, Math.ceil(left)),
          );
          if (terminal || generation !== epoch) continue;
          if (performance.now() >= deadline) {
            stop("deadline");
            break;
          }
          const snapshot = parse(raw, target),
            key = JSON.stringify(snapshot);
          if (key !== prior) {
            prior = key;
            yield snapshot;
          }
        } catch (error) {
          if (terminal || generation !== epoch) continue;
          if (retryRead?.(error)) {
            prior = undefined;
            continue;
          }
          const code = isObject(error) ? error.code : undefined;
          stop(
            code === "unauthorized" ||
              code === "invalid_snapshot" ||
              code === "deadline"
              ? code
              : "snapshot_failed",
          );
        } finally {
          reader = undefined;
        }
      }
      if (terminal && terminal !== "closed")
        throw new LiveWorkInboxError(terminal);
    } finally {
      stop("closed");
      close = undefined;
    }
  }
  const iterator = stream();
  return {
    next: () => iterator.next(),
    return: () => {
      close?.("closed");
      return iterator.return(undefined);
    },
    [Symbol.asyncIterator]() {
      return this;
    },
  };
}
