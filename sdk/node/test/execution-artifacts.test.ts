import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { decodeExecutionArtifact } from '../src/index.js';

test('decodeExecutionArtifact verifies binary outputs and rejects corrupt receipts', () => {
  const bytes = Buffer.from([0, 1, 255]);
  const artifact = { name: 'result.bin', size_bytes: 3, content: bytes.toString('base64'),
    sha256: `sha256:${createHash('sha256').update(bytes).digest('hex')}` };
  assert.deepEqual(decodeExecutionArtifact(artifact), bytes);
  assert.throws(() => decodeExecutionArtifact({ ...artifact, content: 'invalid!' }));
  assert.throws(() => decodeExecutionArtifact({ ...artifact, size_bytes: 4 }));
  assert.throws(() => decodeExecutionArtifact({ ...artifact, sha256: 'sha256:bad' }));
});
