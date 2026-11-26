import { createHmac, randomBytes, randomUUID, timingSafeEqual } from 'node:crypto';
import { constants } from 'node:fs';
import { link, mkdir, open, realpath, unlink, type FileHandle } from 'node:fs/promises';
import { basename, dirname, join, resolve } from 'node:path';
import { isMcpTaskId } from './mcp-task-inbox.js';
import { loadMcpTaskReceiptReference, loadPlannedMcpTaskReceipt } from './mcp-task-receipt.js';
import { isSubmissionKey } from './mcp-task-submission.js';

const manifestName = 'group.json';
const credentialName = 'credential';
const stoppedName = 'stopped';
export const maxTaskGroupMembers = 64;
const maxManifestBytes = 128 * 1024;
const directoryFlags = constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW;
const fileFlags = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK;
const digestPattern = /^[0-9a-f]{64}(?![\s\S])/u;

const messages = {
  invalid_group: 'Invalid or non-private MCP Task group',
  group_exists: 'MCP Task group already exists',
  group_changed: 'MCP Task group changed since this update was delivered',
  group_io: 'Could not read or write the MCP Task group',
  group_stopped: 'MCP Task group was stopped',
  submission_unavailable: 'MCP Task group was not created for submission',
  credential_mismatch: 'MCP Task group submission requires its original bearer credential',
} as const;

export type McpTaskGroupFailureCode = keyof typeof messages;

export class McpTaskGroupError extends Error {
  override readonly name = 'McpTaskGroupError';
  constructor(readonly code: McpTaskGroupFailureCode) { super(messages[code]); }
}

type Member = { submissionKey: string; taskId?: string };
type ManifestBase = { id: string; endpointSha256: string };
type SavedGroupManifest = ManifestBase & { version: 1; members: Member[] };
export type TaskGroupSubmissionMember = { submissionKey: string; requestSha256: string; taskId?: never };
type BearerSubmissionManifest = ManifestBase & { version: 2; members: TaskGroupSubmissionMember[] };
type OwnerSubmissionManifest = ManifestBase & { version: 3; members: TaskGroupSubmissionMember[]; ownerBinding: string };
export type TaskGroupSubmissionManifest = BearerSubmissionManifest | OwnerSubmissionManifest;
export type TaskGroupManifest = SavedGroupManifest | TaskGroupSubmissionManifest;
type TaskGroupInput = Omit<SavedGroupManifest, 'id'> | Omit<BearerSubmissionManifest, 'id'> | Omit<OwnerSubmissionManifest, 'id'>;

function validDigest(value: unknown): value is string {
  return typeof value === 'string' && digestPattern.test(value);
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function invalid(): never { throw new McpTaskGroupError('invalid_group'); }

function uniqueMembers(members: Member[]): boolean {
  const knownIds = members.flatMap(member => member.taskId === undefined ? [] : [member.taskId]);
  return new Set(members.map(member => member.submissionKey)).size === members.length &&
    new Set(knownIds).size === knownIds.length;
}

function decodeManifest(bytes: Uint8Array): TaskGroupManifest {
  let value: unknown;
  try {
    value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
  } catch {
    return invalid();
  }
  if (!record(value) || (value.version !== 1 && value.version !== 2 && value.version !== 3) ||
      Object.keys(value).length !== (value.version === 3 ? 5 : 4) ||
      !validDigest(value.id) || !validDigest(value.endpointSha256) ||
      !Array.isArray(value.members) || value.members.length === 0 || value.members.length > maxTaskGroupMembers) invalid();
  const { id, endpointSha256 } = value;
  if (value.version !== 1) {
    const members = value.members.map((member): TaskGroupSubmissionMember => {
      if (!record(member) || Object.keys(member).length !== 2 ||
          !isSubmissionKey(member.submissionKey) || !validDigest(member.requestSha256)) return invalid();
      return { submissionKey: member.submissionKey, requestSha256: member.requestSha256 };
    });
    if (!uniqueMembers(members)) invalid();
    if (value.version === 3) {
      if (!validDigest(value.ownerBinding)) invalid();
      return { version: 3, id, endpointSha256, members, ownerBinding: value.ownerBinding };
    }
    return { version: 2, id, endpointSha256, members };
  }
  const members: Member[] = value.members.map(member => {
    if (!record(member) || !isSubmissionKey(member.submissionKey) ||
        !Object.keys(member).every(key => key === 'submissionKey' || key === 'taskId')) return invalid();
    if (!Object.hasOwn(member, 'taskId')) return { submissionKey: member.submissionKey };
    if (!isMcpTaskId(member.taskId)) return invalid();
    return { submissionKey: member.submissionKey, taskId: member.taskId };
  });
  if (!uniqueMembers(members)) invalid();
  return { version: 1, id, endpointSha256, members };
}

function fsCode(error: unknown, code: string): boolean {
  return record(error) && error.code === code;
}

async function groupIo<Result>(run: () => Promise<Result>): Promise<Result> {
  try {
    return await run();
  } catch (error) {
    if (error instanceof McpTaskGroupError) throw error;
    if (fsCode(error, 'ELOOP') || fsCode(error, 'ENOTDIR')) invalid();
    throw new McpTaskGroupError('group_io');
  }
}

function privateMode(info: { mode: number; uid: number }): boolean {
  return (info.mode & 0o077) === 0 && (!process.geteuid || info.uid === process.geteuid());
}

async function inGroupDirectory<Result>(path: string, run: (directory: FileHandle) => Promise<Result>): Promise<Result> {
  const directory = await open(path, directoryFlags);
  try {
    if (!privateMode(await directory.stat())) invalid();
    return await run(directory);
  } finally {
    await directory.close();
  }
}

async function readGroupFile(path: string, name: string, maxBytes: number): Promise<Buffer> {
  const file = await open(join(path, name), fileFlags);
  try {
    const info = await file.stat();
    if (!info.isFile() || !privateMode(info) || info.size > maxBytes) invalid();
    const buffer = Buffer.alloc(maxBytes + 1);
    let count = 0;
    while (count < buffer.length) {
      const { bytesRead } = await file.read(buffer, count, buffer.length - count, count);
      if (bytesRead === 0) break;
      count += bytesRead;
    }
    if (count > maxBytes) invalid();
    return buffer.subarray(0, count);
  } finally {
    await file.close();
  }
}

async function readManifest(path: string): Promise<TaskGroupManifest> {
  return decodeManifest(await readGroupFile(path, manifestName, maxManifestBytes));
}

async function canonicalGroupPath(path: string): Promise<string> {
  if (typeof path !== 'string' || !path.trim()) invalid();
  const absolute = resolve(path);
  if (dirname(absolute) === absolute) invalid();
  return join(await realpath(dirname(absolute)), basename(absolute));
}

async function publishGroupFile(path: string, name: string, content: string): Promise<boolean> {
  const temporary = join(path, `.group-${randomUUID()}`);
  const file = await open(temporary, 'wx', 0o600);
  try {
    try {
      await file.chmod(0o600);
      await file.writeFile(content);
      await file.sync();
    } finally {
      await file.close();
    }
    try {
      await link(temporary, join(path, name));
      return true;
    } catch (error) {
      if (fsCode(error, 'EEXIST')) return false;
      throw error;
    }
  } finally {
    await unlink(temporary).catch(() => undefined);
  }
}

/** Save the read-only references from existing receipts as an immutable group. */
export async function createMcpTaskGroup(path: string, receiptPaths: readonly string[]): Promise<void> {
  if (!Array.isArray(receiptPaths) || receiptPaths.length === 0 || receiptPaths.length > maxTaskGroupMembers) invalid();
  const receipts = [];
  for (const receiptPath of [...receiptPaths]) receipts.push(await loadMcpTaskReceiptReference(receiptPath));
  const endpointSha256 = receipts[0]!.endpointSha256;
  const members = receipts.map(({ submissionKey, taskId }) => ({
    submissionKey, ...(taskId === undefined ? {} : { taskId }),
  }));
  if (receipts.some(receipt => receipt.endpointSha256 !== endpointSha256) || !uniqueMembers(members)) invalid();
  await createTaskGroupFile(path, { version: 1, endpointSha256, members });
}

export function newTaskGroupId(): string {
  return randomBytes(32).toString('hex');
}

export function createTaskGroupFile<Input extends TaskGroupInput>(
  path: string, input: Input, id = newTaskGroupId(),
): Promise<{ path: string; manifest: Input & { id: string } }> {
  const manifest = { ...input, id };
  return groupIo(async () => {
    const file = await canonicalGroupPath(path);
    const parent = await open(dirname(file), directoryFlags);
    try {
      const { mode } = await parent.stat();
      if ((mode & 0o022) !== 0 && (mode & 0o1000) === 0) invalid();
      await mkdir(file, { mode: 0o700 }).catch((error: unknown) => { if (!fsCode(error, 'EEXIST')) throw error; });
      await inGroupDirectory(file, async directory => {
        if (!await publishGroupFile(file, manifestName, JSON.stringify(manifest) + '\n')) {
          throw new McpTaskGroupError('group_exists');
        }
        await directory.sync();
        await parent.sync();
      });
      return { path: file, manifest };
    } finally {
      await parent.close();
    }
  });
}

export function loadTaskGroup(path: string): Promise<{ path: string; manifest: TaskGroupManifest }> {
  return groupIo(async () => {
    const file = await canonicalGroupPath(path);
    return { path: file, manifest: await inGroupDirectory(file, () => readManifest(file)) };
  });
}

export async function loadTaskGroupSubmission(path: string): Promise<{ path: string; manifest: TaskGroupSubmissionManifest }> {
  const group = await loadTaskGroup(path);
  if (group.manifest.version === 1) throw new McpTaskGroupError('submission_unavailable');
  return { path: group.path, manifest: group.manifest };
}

export function taskGroupReceiptPath(path: string, receiptIndex: number): string {
  return join(path, `member-${receiptIndex}.json`);
}

export async function readTaskGroupMemberReference(
  path: string, manifest: TaskGroupManifest, receiptIndex: number,
): Promise<Member | undefined> {
  if (manifest.version === 1) return manifest.members[receiptIndex]!;
  return loadPlannedMcpTaskReceipt(taskGroupReceiptPath(path, receiptIndex), {
    endpointSha256: manifest.endpointSha256, ...manifest.members[receiptIndex]!,
  });
}

async function readGroupDigest(path: string, name: string): Promise<string | undefined> {
  const bytes = await readGroupFile(path, name, 64).catch((error: unknown) => {
    if (fsCode(error, 'ENOENT')) return undefined;
    throw error;
  });
  if (!bytes) return undefined;
  const proof = bytes.toString('latin1');
  if (!validDigest(proof)) invalid();
  return proof;
}

async function requireGroupIdentity(path: string, groupId: string): Promise<void> {
  if ((await readManifest(path)).id !== groupId) throw new McpTaskGroupError('group_changed');
}

async function checkGroupOpen(path: string, groupId: string): Promise<void> {
  await requireGroupIdentity(path, groupId);
  const stopped = await readGroupDigest(path, stoppedName);
  if (stopped === groupId) throw new McpTaskGroupError('group_stopped');
  if (stopped !== undefined) throw new McpTaskGroupError('group_changed');
}

export function requireTaskGroupOpen(path: string, groupId: string): Promise<void> {
  return groupIo(() => inGroupDirectory(path, () => checkGroupOpen(path, groupId)));
}

export function recordTaskGroupStop(path: string, groupId: string): Promise<void> {
  return groupIo(() => inGroupDirectory(path, async directory => {
    await requireGroupIdentity(path, groupId);
    let stopped = await readGroupDigest(path, stoppedName);
    if (stopped === undefined) {
      stopped = await publishGroupFile(path, stoppedName, groupId) ? groupId : await readGroupDigest(path, stoppedName);
    }
    if (stopped !== groupId) throw new McpTaskGroupError('group_changed');
    await directory.sync();
  }));
}

// The first outgoing request fixes the group bearer for every member. Reuse
// the private, exclusive publication path so competing processes agree.
export async function bindTaskGroupCredential(
  path: string, manifest: TaskGroupSubmissionManifest, credential: string | undefined,
): Promise<void> {
  if (credential === undefined) throw new McpTaskGroupError('credential_mismatch');
  const proof = createHmac('sha256', credential).update('gregale/node-sdk/mcp-task-group/v2:')
    .update(manifest.id).update(manifest.endpointSha256).digest('hex');
  await groupIo(() => inGroupDirectory(path, async directory => {
    await checkGroupOpen(path, manifest.id);
    let saved = await readGroupDigest(path, credentialName);
    if (saved === undefined) {
      saved = await publishGroupFile(path, credentialName, proof) ? proof : await readGroupDigest(path, credentialName);
    }
    if (saved === undefined || !timingSafeEqual(Buffer.from(saved, 'hex'), Buffer.from(proof, 'hex'))) {
      throw new McpTaskGroupError('credential_mismatch');
    }
    await directory.sync();
  }));
}

function ackPath(path: string, update: string): string {
  return join(path, `ack-${update}`);
}

async function openAcknowledgment(file: string): Promise<FileHandle | undefined> {
  const marker = await open(file, fileFlags).catch((error: unknown) => {
    if (!fsCode(error, 'ENOENT')) throw error;
    return undefined;
  });
  if (!marker) return undefined;
  try {
    const info = await marker.stat();
    if (!info.isFile() || !privateMode(info) || info.size !== 0) invalid();
    return marker;
  } catch (error) {
    await marker.close();
    throw error;
  }
}

export function taskGroupUpdateAcknowledged(path: string, update: string): Promise<boolean> {
  return groupIo(() => inGroupDirectory(path, async () => {
    const marker = await openAcknowledgment(ackPath(path, update));
    if (!marker) return false;
    await marker.close();
    return true;
  }));
}

export function acknowledgeTaskGroupUpdate(path: string, groupId: string, update: string): Promise<void> {
  return groupIo(() => inGroupDirectory(path, async directory => {
    await requireGroupIdentity(path, groupId);
    const file = ackPath(path, update);
    const marker = await open(file, 'wx', 0o600).catch(async (error: unknown) => {
      if (!fsCode(error, 'EEXIST')) throw error;
      const saved = await openAcknowledgment(file);
      if (!saved) throw new McpTaskGroupError('group_changed');
      return saved;
    });
    try {
      await marker.chmod(0o600);
      await marker.sync();
    } finally {
      await marker.close();
    }
    await directory.sync();
  }));
}
