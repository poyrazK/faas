import {
  bindTaskGroupCredential, createTaskGroupFile, loadTaskGroupSubmission, maxTaskGroupMembers,
  McpTaskGroupError, newTaskGroupId, requireTaskGroupOpen, taskGroupReceiptPath, type TaskGroupSubmissionManifest,
} from './mcp-task-group-file.js';
import { groupConnections } from './mcp-task-group.js';
import type { McpTaskInboxEntry } from './mcp-task-inbox.js';
import {
  loadPlannedMcpTaskReceipt, mcpTaskReceiptEndpoint, McpTaskReceiptError, mcpTaskReceiptScope, prepareMcpTaskReceiptSubmission,
  sendPlannedMcpTaskReceipt, type McpTaskReceiptConnection, type McpTaskReceiptSubmission,
  type PreparedMcpTaskReceipt,
} from './mcp-task-receipt.js';
import { McpTaskReadError, readMcpTaskMetadata } from './mcp-task-result.js';
import {
  lookupMcpTaskBySubmissionKey, McpTaskSubmissionError, requestTaskGroupOwnerBinding,
  supportsTaskGroupOwnerBinding, type McpTaskSubmissionOptions,
} from './mcp-task-submission.js';

type SentMember = { readonly receiptIndex: number } & (
  | { readonly outcome: 'accepted'; readonly task: McpTaskInboxEntry }
  | { readonly outcome: 'unconfirmed' }
);
export type McpTaskGroupSubmissionEntry = SentMember | (
  { readonly receiptIndex: number } & (
    | { readonly outcome: 'prepared' }
    | { readonly outcome: 'unavailable'; readonly error: McpTaskReadError | McpTaskSubmissionError }
  )
);

type SubmissionGroup = { path: string; manifest: TaskGroupSubmissionManifest };
type Receipt = Awaited<ReturnType<typeof loadPlannedMcpTaskReceipt>>;

function plannedMember(group: SubmissionGroup, receiptIndex: number) {
  const { endpointSha256, members } = group.manifest;
  return {
    path: taskGroupReceiptPath(group.path, receiptIndex),
    plan: { endpointSha256, ...members[receiptIndex]! },
  };
}

async function inspectMember(
  group: SubmissionGroup, receiptIndex: number, clients: ReturnType<typeof groupConnections>,
  operation: Required<McpTaskSubmissionOptions>,
): Promise<{ entry: McpTaskGroupSubmissionEntry; receipt?: NonNullable<Receipt> }> {
  operation.signal.throwIfAborted();
  const { path, plan } = plannedMember(group, receiptIndex);
  const receipt = await loadPlannedMcpTaskReceipt(path, plan);
  operation.signal.throwIfAborted();
  clients.check();
  if (!receipt) return { entry: { receiptIndex, outcome: 'prepared' } };
  try {
    const task = receipt.taskId === undefined
      ? await lookupMcpTaskBySubmissionKey(clients.lookup, receipt.submissionKey, operation)
      : await readMcpTaskMetadata(clients.tasks, receipt.taskId, operation);
    clients.check();
    return { entry: { receiptIndex, outcome: 'accepted', task }, receipt };
  } catch (error) {
    operation.signal.throwIfAborted();
    clients.check();
    if (error instanceof McpTaskSubmissionError && error.code === 'not_found' && receipt.taskId === undefined) {
      return { entry: { receiptIndex, outcome: 'unconfirmed' }, receipt };
    }
    if (!(error instanceof McpTaskSubmissionError || error instanceof McpTaskReadError)) throw error;
    return { entry: { receiptIndex, outcome: 'unavailable', error }, receipt };
  }
}

async function sendMember(
  connection: McpTaskReceiptConnection, group: SubmissionGroup, receiptIndex: number,
  prepared: PreparedMcpTaskReceipt, operation: Required<McpTaskSubmissionOptions>, receipt?: Receipt, refreshExpired = false,
): Promise<SentMember> {
  const { path, plan } = plannedMember(group, receiptIndex);
  const { manifest } = group;
  const owner = manifest.version === 3 ? { groupId: manifest.id, binding: manifest.ownerBinding } : undefined;
  if (owner && !supportsTaskGroupOwnerBinding(connection)) throw new McpTaskSubmissionError('unavailable');
  const ensureOpen = () => requireTaskGroupOpen(group.path, manifest.id);
  const result = await sendPlannedMcpTaskReceipt(connection, path, plan, prepared, operation, {
    authorize: credential => {
      if (!owner) return bindTaskGroupCredential(group.path, manifest, credential);
      if (!credential) throw new McpTaskGroupError('credential_mismatch');
      return ensureOpen();
    },
    ensureOpen,
    owner, receipt, refreshExpired,
  });
  return result.outcome === 'accepted' ? { receiptIndex, outcome: 'accepted', task: result.task }
    : { receiptIndex, outcome: 'unconfirmed' };
}

/** Publish the full group plan before submitting its members in order. */
export async function submitMcpTaskGroup(
  connection: McpTaskReceiptConnection, path: string, submissions: readonly McpTaskReceiptSubmission[],
  options: McpTaskSubmissionOptions = {},
): Promise<readonly McpTaskGroupSubmissionEntry[]> {
  const operation = mcpTaskReceiptScope(options);
  operation.signal.throwIfAborted();
  if (!Array.isArray(submissions) || submissions.length === 0 || submissions.length > maxTaskGroupMembers) {
    throw new McpTaskGroupError('invalid_group');
  }
  const endpointSha256 = mcpTaskReceiptEndpoint(connection);
  const clients = groupConnections(connection, endpointSha256);
  const prepared = Array.from(submissions, submission => prepareMcpTaskReceiptSubmission(connection, submission));
  const members = prepared.map(({ submissionKey, requestSha256 }) => ({ submissionKey, requestSha256 }));
  const groupId = newTaskGroupId();
  const ownerBinding = supportsTaskGroupOwnerBinding(clients.lookup)
    ? await requestTaskGroupOwnerBinding(clients.lookup, groupId, operation) : undefined;
  clients.check();
  const group = ownerBinding === undefined
    ? await createTaskGroupFile(path, { version: 2, endpointSha256, members }, groupId)
    : await createTaskGroupFile(path, { version: 3, endpointSha256, members, ownerBinding }, groupId);
  const entries: McpTaskGroupSubmissionEntry[] = [];
  for (const [receiptIndex, submission] of prepared.entries()) {
    clients.check();
    const entry = await sendMember(connection, group, receiptIndex, submission, operation);
    entries.push(entry);
    if (entry.outcome === 'unconfirmed') break;
  }
  for (let receiptIndex = entries.length; receiptIndex < members.length; receiptIndex++) {
    entries.push({ receiptIndex, outcome: 'prepared' });
  }
  return entries;
}

/** Read admission evidence for a saved group using the current authorization. */
export async function resumeMcpTaskGroupSubmission(
  connection: McpTaskReceiptConnection, path: string, options: McpTaskSubmissionOptions = {},
): Promise<readonly McpTaskGroupSubmissionEntry[]> {
  const operation = mcpTaskReceiptScope(options);
  operation.signal.throwIfAborted();
  const group = await loadTaskGroupSubmission(path);
  const clients = groupConnections(connection, group.manifest.endpointSha256);
  const entries: McpTaskGroupSubmissionEntry[] = [];
  for (let start = 0; start < group.manifest.members.length; start += 4) {
    const batch = await Promise.all(group.manifest.members.slice(start, start + 4).map(
      (_, offset) => inspectMember(group, start + offset, clients, operation)));
    for (const { entry } of batch) entries.push(entry);
  }
  return entries;
}

/** Continue one saved member, refreshing an expired key only before its first recorded attempt. */
export async function continueMcpTaskGroupSubmission(
  connection: McpTaskReceiptConnection, path: string, receiptIndex: number, submission: McpTaskReceiptSubmission,
  options: McpTaskSubmissionOptions = {},
): Promise<SentMember> {
  const operation = mcpTaskReceiptScope(options);
  operation.signal.throwIfAborted();
  const group = await loadTaskGroupSubmission(path);
  const member = group.manifest.members[receiptIndex];
  if (!Number.isInteger(receiptIndex) || receiptIndex < 0 || !member) throw new RangeError('Invalid MCP Task group receipt index');
  const clients = groupConnections(connection, group.manifest.endpointSha256);
  await requireTaskGroupOpen(group.path, group.manifest.id);
  clients.check();
  const prepared = prepareMcpTaskReceiptSubmission(connection, submission, member.submissionKey);
  if (prepared.requestSha256 !== member.requestSha256) throw new McpTaskReceiptError('request_mismatch');
  const { entry, receipt } = await inspectMember(group, receiptIndex, clients, operation);
  if (entry.outcome === 'accepted') return entry;
  if (entry.outcome === 'unavailable') {
    // A lookup can fail while an existing attempt is in flight. Only a
    // definite not-found response permits an explicit same-key retry.
    if (entry.error instanceof McpTaskSubmissionError && entry.error.code === 'request_failed' &&
        entry.error.status === undefined) return { receiptIndex, outcome: 'unconfirmed' };
    throw entry.error;
  }
  clients.check();
  try {
    return await sendMember(connection, group, receiptIndex, prepared, operation, receipt, true);
  } catch (error) {
    if (!receipt && error instanceof McpTaskReceiptError && error.code === 'receipt_exists') {
      // Another caller recorded the first attempt. Leave its key in place and
      // observe the winner without sending a replacement or automatic retry.
      const current = await inspectMember(group, receiptIndex, clients, operation);
      if (current.entry.outcome === 'accepted') return current.entry;
      if (current.receipt) return { receiptIndex, outcome: 'unconfirmed' };
    }
    throw error;
  }
}
