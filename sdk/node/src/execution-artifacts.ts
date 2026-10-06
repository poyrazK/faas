import { createHash } from 'node:crypto';
import type { ExecutionArtifact } from './generated/models/ExecutionArtifact.js';

/** Decode one inline artifact and verify its size and checksum before use.
 * This helper never writes files or creates a persistent workspace.
 */
export function decodeExecutionArtifact(artifact: ExecutionArtifact): Uint8Array {
  const bytes = Buffer.from(artifact.content, 'base64');
  if (bytes.toString('base64') !== artifact.content || bytes.length !== artifact.size_bytes
    || `sha256:${createHash('sha256').update(bytes).digest('hex')}` !== artifact.sha256) {
    throw new Error('execution artifact content failed integrity verification');
  }
  return bytes;
}
