import { loadTaskGroup, readTaskGroupMemberReference, recordTaskGroupStop } from './mcp-task-group-file.js';
import { groupConnections, type McpTaskGroupConnection } from './mcp-task-group.js';
import { McpTaskReceiptError, mcpTaskReceiptScope } from './mcp-task-receipt.js';
import {
  cancelMcpTask, McpTaskCancellationError, McpTaskReadError, type McpTaskCancellationConnection,
  type McpTaskCancellationOutcome, type McpTaskReadOptions,
} from './mcp-task-result.js';
import { lookupMcpTaskBySubmissionKey, McpTaskSubmissionError } from './mcp-task-submission.js';

export type McpTaskGroupStopConnection = McpTaskGroupConnection & McpTaskCancellationConnection;

export type McpTaskGroupStopEntry = { readonly receiptIndex: number } & (
  | McpTaskCancellationOutcome
  | { readonly outcome: 'not_submitted' | 'admission_unconfirmed' }
  | { readonly outcome: 'unavailable'; readonly error:
    McpTaskReadError | McpTaskSubmissionError | McpTaskCancellationError | McpTaskReceiptError }
);

/** Persist the stop before one reconciliation and cancellation pass. Repeat to resume after interruption. */
export async function stopMcpTaskGroup(
  connection: McpTaskGroupStopConnection, path: string, options: McpTaskReadOptions = {},
): Promise<readonly McpTaskGroupStopEntry[]> {
  const operation = mcpTaskReceiptScope(options);
  operation.signal.throwIfAborted();
  const group = await loadTaskGroup(path);
  const { tasks, lookup, check } = groupConnections(connection, group.manifest.endpointSha256);
  const cancellation: McpTaskCancellationConnection = {
    getNegotiatedProtocolVersion: tasks.getNegotiatedProtocolVersion,
    getServerCapabilities: tasks.getServerCapabilities,
    request(...args) { check(); return connection.request(...args); },
  };
  operation.signal.throwIfAborted();
  await recordTaskGroupStop(group.path, group.manifest.id);
  check();

  async function stopMember(receiptIndex: number): Promise<McpTaskGroupStopEntry> {
    operation.signal.throwIfAborted();
    try {
      const receipt = await readTaskGroupMemberReference(group.path, group.manifest, receiptIndex);
      if (!receipt) return { receiptIndex, outcome: 'not_submitted' };
      const taskId = receipt.taskId ??
        (await lookupMcpTaskBySubmissionKey(lookup, receipt.submissionKey, operation)).taskId;
      const outcome = await cancelMcpTask(cancellation, taskId, operation);
      check();
      return { receiptIndex, ...outcome };
    } catch (error) {
      operation.signal.throwIfAborted();
      check();
      if (error instanceof McpTaskSubmissionError &&
          (error.code === 'not_found' || (error.code === 'request_failed' && error.status === undefined))) {
        return { receiptIndex, outcome: 'admission_unconfirmed' };
      }
      if (error instanceof McpTaskReadError || error instanceof McpTaskSubmissionError ||
          error instanceof McpTaskCancellationError || error instanceof McpTaskReceiptError) {
        return { receiptIndex, outcome: 'unavailable', error };
      }
      throw error;
    }
  }

  const entries: McpTaskGroupStopEntry[] = [];
  for (let start = 0; start < group.manifest.members.length; start += 4) {
    const batch = await Promise.all(group.manifest.members.slice(start, start + 4)
      .map((_, offset) => stopMember(start + offset)));
    entries.push(...batch);
  }
  return entries;
}
