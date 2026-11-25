import {
  parseLiveWorkInboxSnapshot,
  workInboxWatchTarget,
  type LiveWorkInboxSnapshot,
  type WorkInboxWatchTarget,
} from "./live-work-inbox.js";
import type { QueryOptions, QueryTool } from "./query-tools.js";
import type { WorkInboxEntry, WorkInboxFilter } from "./work-inbox.js";

export interface WorkInboxSnapshotOptions extends QueryOptions {
  readonly maxPages?: number;
  readonly maxItems?: number;
}
/** Read the business record's inbox through its existing current authorization. */
export async function readWorkInboxSnapshot<A>(
  inbox: Pick<QueryTool<WorkInboxFilter, WorkInboxEntry, A>, "pages">,
  expected: WorkInboxWatchTarget,
  access: A,
  options: WorkInboxSnapshotOptions,
): Promise<LiveWorkInboxSnapshot> {
  const target = workInboxWatchTarget(expected),
    maxPages = options.maxPages ?? 10,
    maxItems = options.maxItems ?? 100;
  if (
    !Number.isSafeInteger(maxPages) ||
    maxPages < 1 ||
    maxPages > 100 ||
    !Number.isSafeInteger(maxItems) ||
    maxItems < 1 ||
    maxItems > 100
  )
    throw new TypeError("Invalid work inbox snapshot limits");
  const items: WorkInboxEntry[] = [];
  let truncated = true;
  for await (const page of inbox.pages(
    { business_ref: target.business_ref },
    access,
    { ...options, maxPages, maxItems },
  )) {
    items.push(...page.items);
    truncated = page.next_cursor !== null;
  }
  return parseLiveWorkInboxSnapshot(
    { version: 1, ...target, items, truncated },
    target,
  );
}
