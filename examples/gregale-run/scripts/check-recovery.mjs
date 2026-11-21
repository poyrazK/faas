import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export function checkRecovery(parentWorkflow, childWorkflow, parentID, childID) {
  const parent = parentWorkflow.managed?.find(job => job.id === parentID)?.pipeline?.completion;
  const child = childWorkflow.managed?.find(job => job.id === childID)?.pipeline?.completion;
  assert.ok(parent && child, 'Fetch terminal workflow receipts for the exact accepted pipeline IDs');
  assert.equal(parent.completion_id, parentID);
  assert.equal(child.completion_id, childID);
  assert.equal(parent.outcome, 'failed', 'Parent must have stopped at the budget refusal');
  assert.equal(child.outcome, 'succeeded');
  assert.equal(child.rerun?.parent_id, parentID);
  assert.deepEqual(parent.steps.map(s => s.label), ['north', 'south', 'merge']);
  assert.deepEqual(child.steps.map(s => s.label), ['north', 'south', 'merge']);
  for (const label of ['north', 'south']) {
    const before = parent.steps.find(s => s.label === label), after = child.steps.find(s => s.label === label);
    assert.equal(before.outcome, 'succeeded');
    assert.equal(before.attempts.length, 1);
    assert.equal(after.outcome, 'succeeded');
    assert.equal(after.reused_run_id, before.attempts[0].run_id);
    assert.equal(after.output_sha256, before.output_sha256);
    assert.match(after.output_sha256, /^[a-f0-9]{64}$/);
    assert.equal(after.attempts.length, 0, 'Reused work must not claim a new attempt');
  }
  const before = parent.steps.find(s => s.label === 'merge'), after = child.steps.find(s => s.label === 'merge');
  assert.notEqual(before.outcome, 'succeeded');
  assert.equal(before.attempts.length, 0, 'The budget must refuse merge before admission');
  assert.equal(after.outcome, 'succeeded');
  assert.equal(after.attempts.length, 1);
  assert.equal(after.attempts[0].status, 'succeeded');
  assert.ok(after.attempts[0].run_id);
  assert.ok(!after.reused_run_id);
  return { reused_reports: 2, new_attempts: 1, merge_run_id: after.attempts[0].run_id };
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const [parentPath, childPath, parentID, childID] = process.argv.slice(2);
  if (!parentPath || !childPath || !parentID || !childID || process.argv.length !== 6) throw new Error('Usage: node scripts/check-recovery.mjs parent.json child.json PARENT_PIPELINE_ID CHILD_PIPELINE_ID');
  console.log(JSON.stringify(checkRecovery(JSON.parse(await fs.readFile(parentPath)), JSON.parse(await fs.readFile(childPath)), parentID, childID), null, 2));
}
