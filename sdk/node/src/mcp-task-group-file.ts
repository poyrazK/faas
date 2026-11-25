import { randomBytes, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { link, mkdir, open, realpath, unlink, type FileHandle } from 'node:fs/promises';
import { basename, dirname, join, resolve } from 'node:path';
import { isMcpTaskId } from './mcp-task-inbox.js';
import { loadMcpTaskReceiptReference } from './mcp-task-receipt.js';
import { isSubmissionKey } from './mcp-task-submission.js';

const manifestName = 'group.json';
const maxMembers = 64;
const maxManifestBytes = 128 * 1024;
const directoryFlags = constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW;
const fileFlags = constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK;
const digestPattern = /^[0-9a-f]{64}(?![\s\S])/u;

const messages = {
  invalid_group: 'Invalid or non-private MCP Task group',
  group_exists: 'MCP Task group already exists',
  group_changed: 'MCP Task group changed since this update was delivered',
  group_io: 'Could not read or write the MCP Task group',
} as const;

export type McpTaskGroupFailureCode = keyof typeof messages;

export class McpTaskGroupError extends Error {
  override readonly name = 'McpTaskGroupError';
  constructor(readonly code: McpTaskGroupFailureCode) { super(messages[code]); }
}

type Member = { submissionKey: string; taskId?: string };
export type TaskGroupManifest = { version: 1; id: string; endpointSha256: string; members: Member[] };

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
  if (!record(value) || Object.keys(value).length !== 4 || value.version !== 1 ||
      !validDigest(value.id) || !validDigest(value.endpointSha256) ||
      !Array.isArray(value.members) || value.members.length === 0 || value.members.length > maxMembers) invalid();
  const members: Member[] = value.members.map(member => {
    if (!record(member) || !isSubmissionKey(member.submissionKey) ||
        !Object.keys(member).every(key => key === 'submissionKey' || key === 'taskId')) return invalid();
    if (!Object.hasOwn(member, 'taskId')) return { submissionKey: member.submissionKey };
    if (!isMcpTaskId(member.taskId)) return invalid();
    return { submissionKey: member.submissionKey, taskId: member.taskId };
  });
  if (!uniqueMembers(members)) invalid();
  return { version: 1, id: value.id, endpointSha256: value.endpointSha256, members };
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

async function readManifest(path: string): Promise<TaskGroupManifest> {
  const file = await open(join(path, manifestName), fileFlags);
  try {
    const info = await file.stat();
    if (!info.isFile() || !privateMode(info) || info.size > maxManifestBytes) invalid();
    const buffer = Buffer.alloc(maxManifestBytes + 1);
    let count = 0;
    while (count < buffer.length) {
      const { bytesRead } = await file.read(buffer, count, buffer.length - count, count);
      if (bytesRead === 0) break;
      count += bytesRead;
    }
    if (count > maxManifestBytes) invalid();
    return decodeManifest(buffer.subarray(0, count));
  } finally {
    await file.close();
  }
}

async function canonicalGroupPath(path: string): Promise<string> {
  if (typeof path !== 'string' || !path.trim()) invalid();
  const absolute = resolve(path);
  if (dirname(absolute) === absolute) invalid();
  return join(await realpath(dirname(absolute)), basename(absolute));
}

async function publishManifest(path: string, manifest: TaskGroupManifest): Promise<void> {
  const temporary = join(path, `.group-${randomUUID()}`);
  const file = await open(temporary, 'wx', 0o600);
  try {
    try {
      await file.chmod(0o600);
      await file.writeFile(JSON.stringify(manifest) + '\n');
      await file.sync();
    } finally {
      await file.close();
    }
    try {
      await link(temporary, join(path, manifestName));
    } catch (error) {
      if (fsCode(error, 'EEXIST')) throw new McpTaskGroupError('group_exists');
      throw error;
    }
  } finally {
    await unlink(temporary).catch(() => undefined);
  }
}

/** Save the read-only references from existing receipts as an immutable group. */
export async function createMcpTaskGroup(path: string, receiptPaths: readonly string[]): Promise<void> {
  if (!Array.isArray(receiptPaths) || receiptPaths.length === 0 || receiptPaths.length > maxMembers) invalid();
  const receipts = [];
  for (const receiptPath of [...receiptPaths]) receipts.push(await loadMcpTaskReceiptReference(receiptPath));
  const endpointSha256 = receipts[0]!.endpointSha256;
  const members = receipts.map(({ submissionKey, taskId }) => ({
    submissionKey, ...(taskId === undefined ? {} : { taskId }),
  }));
  if (receipts.some(receipt => receipt.endpointSha256 !== endpointSha256) || !uniqueMembers(members)) invalid();
  const manifest: TaskGroupManifest = { version: 1, id: randomBytes(32).toString('hex'), endpointSha256, members };
  await groupIo(async () => {
    const file = await canonicalGroupPath(path);
    const parent = await open(dirname(file), directoryFlags);
    try {
      const { mode } = await parent.stat();
      if ((mode & 0o022) !== 0 && (mode & 0o1000) === 0) invalid();
      await mkdir(file, { mode: 0o700 }).catch((error: unknown) => { if (!fsCode(error, 'EEXIST')) throw error; });
      await inGroupDirectory(file, async directory => {
        await publishManifest(file, manifest);
        await directory.sync();
        await parent.sync();
      });
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
    if ((await readManifest(path)).id !== groupId) throw new McpTaskGroupError('group_changed');
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
