import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {browserStarter, csv, definitionID, replacementID} from './fixtures/customer-operation-starter-browser.mjs';

// Real Chromium loads the generated starter and its packed browser SDK. Only
// control-plane responses are fixtures; these tests establish no native outcome.
test('workflow UI restores lost acceptance after reload without submitting again and isolates customers', async t => {
  const f = await browserStarter(t, 'customer-operation-workflow-export'); f.api.lost = 'after_commit';
  await f.signin(); await f.start();
  await f.page.waitForFunction(() => document.getElementById('error').textContent);
  assert.equal(f.api.submissions.length, 1);
  const id = [...f.api.operations.keys()][0];
  await f.page.reload(); await f.signin();
  await f.page.waitForFunction(id => document.getElementById('identity').textContent === id, id);
  assert.equal(f.api.submissions.length, 1);
  assert.match(await f.page.locator('#notice').innerText(), /restored/);
  const storage = await f.page.evaluate(() => Object.values(localStorage));
  assert.ok(storage.length > 0);
  for (const value of storage) {
    assert.doesNotMatch(value, /alice|bob|"input":|"count":/);
    assert.equal(typeof JSON.parse(value).inputFingerprint, 'string');
  }
  await f.page.locator('#signout').click();
  const watermark = f.api.requests.length;
  await f.signin('bob');
  assert.equal(await f.page.locator('#history li').count(), 0);
  assert.equal(await f.page.locator('#detail').isVisible(), false);
  assert.equal(f.api.requests.slice(watermark).filter(req => req.owner === 'bob' && req.path.includes(id)).length, 0);
  assert.deepEqual(f.errors, []);
});

test('workflow UI streams step progress and downloads success while its notification fails', async t => {
  const f = await browserStarter(t, 'customer-operation-workflow-export'); await f.signin(); await f.start();
  await f.page.locator('#detail').waitFor({state: 'visible'});
  const id = [...f.api.operations.keys()][0];
  f.api.update(id, {progress: {stage: 'transform', completed: 1, total: 3}});
  await f.page.waitForFunction(() => document.querySelector('[aria-current=step]')?.textContent.includes('Transform rows'));
  assert.match(await f.page.locator('#stages li').first().innerText(), /complete/);
  f.api.succeeded(id);
  await f.page.waitForFunction(() => !document.getElementById('download').disabled);
  assert.match(await f.page.locator('#business').innerText(), /succeeded/);
  assert.match(await f.page.locator('#delivery').innerText(), /failed/);
  const download = f.page.waitForEvent('download'); await f.page.locator('#download').click();
  const file = await download;
  assert.equal(file.suggestedFilename(), 'export.csv');
  assert.equal(readFileSync(await file.path(), 'utf8'), csv);
  assert.equal(f.api.downloads, 1); assert.equal(f.api.submissions.length, 1);
  f.api.update(id, {completion_delivery: {state: 'succeeded', attempts: 2}});
  await f.page.waitForFunction(() => document.getElementById('delivery').textContent.includes('succeeded'));
  assert.equal(f.api.submissions.length, 1); assert.deepEqual(f.errors, []);
});

test('workflow UI cancellation retains completed prefix and shows reconciliation without publishing or replaying work', async t => {
  const f = await browserStarter(t, 'customer-operation-workflow-export'); await f.signin(); await f.start();
  await f.page.locator('#detail').waitFor({state: 'visible'});
  const id = [...f.api.operations.keys()][0];
  f.api.update(id, {progress: {stage: 'finish', completed: 2, total: 3}});
  await f.page.waitForFunction(() => document.querySelector('[aria-current=step]')?.textContent.includes('Generate file'));
  await f.page.locator('#cancel').click();
  await f.page.locator('#reconciliation').waitFor({state: 'visible'});
  assert.match(await f.page.locator('#stages li').nth(2).innerText(), /needs review/);
  assert.equal(await f.page.locator('#download').isDisabled(), true);
  assert.equal(await f.page.locator('#cancel').isDisabled(), true);
  assert.equal(f.api.submissions.length, 1); assert.equal(f.api.downloads, 0);
  assert.equal(f.api.requests.filter(req => req.path.endsWith('/cancel')).length, 1);
  assert.equal(f.api.requests.filter(req => /recover|retry|resume/.test(req.path)).length, 0);
  assert.deepEqual(f.errors, []);
});

test('workflow UI freezes uncertain input and definition across reload before explicit retry', async t => {
  const f = await browserStarter(t, 'customer-operation-workflow-export'); f.api.lost = 'before_commit';
  await f.signin(); await f.start();
  await f.page.waitForFunction(() => document.getElementById('error').textContent);
  await f.page.route('**/config', route => route.fulfill({json: {...f.config, definitionID: replacementID}}));
  await f.page.reload(); await f.signin();
  assert.match(await f.page.locator('#notice').innerText(), /unresolved/);
  await f.page.locator('#count').fill('4'); await f.page.locator('#submit').click();
  await f.page.waitForFunction(() => document.getElementById('error').textContent.includes('same input'));
  assert.equal(f.api.submissions.length, 1);
  await f.start(); await f.page.locator('#detail').waitFor({state: 'visible'});
  assert.equal(f.api.submissions.length, 2); assert.equal(f.api.operations.size, 1);
  assert.equal(f.api.submissions[0].key, f.api.submissions[1].key);
  assert.equal(f.api.submissions[1].definition_id, definitionID);
  assert.deepEqual(f.api.submissions[1].input, {count: 3});
  assert.deepEqual(f.errors, []);
});

test('workflow UI checks the typed result artifact identity before downloading', async t => {
  const f = await browserStarter(t, 'customer-operation-workflow-export'); await f.signin(); await f.start();
  await f.page.locator('#detail').waitFor({state: 'visible'});
  f.api.succeeded([...f.api.operations.keys()][0], replacementID);
  await f.page.waitForFunction(() => !document.getElementById('download').disabled);
  await f.page.locator('#download').click();
  await f.page.waitForFunction(() => document.getElementById('error').textContent.includes('retained export file'));
  assert.equal(f.api.downloads, 0); assert.equal(f.api.submissions.length, 1);
  assert.deepEqual(f.errors, []);
});

for (const [kind, template] of [
  ['HTTP', 'customer-operation-export'],
  ['Job', 'customer-operation-job-export'],
]) {
  test(`${kind} starter UI restores lost acceptance after reload and isolates customers`, async t => {
    const f = await browserStarter(t, template); f.api.lost = 'after_commit';
    await f.signin(); await f.start();
    await f.page.waitForFunction(() => document.getElementById('error').textContent);
    assert.equal(f.api.submissions.length, 1);
    const id = [...f.api.operations.keys()][0];
    await f.page.reload(); await f.signin();
    await f.page.waitForFunction(id => document.getElementById('identity').textContent === id, id);
    assert.equal(f.api.submissions.length, 1);
    assert.match(await f.page.locator('#notice').innerText(), /restored/);
    const storage = await f.page.evaluate(() => Object.values(localStorage));
    assert.ok(storage.length > 0);
    for (const value of storage) {
      assert.doesNotMatch(value, /alice|bob|"input":|"count":/);
      assert.equal(typeof JSON.parse(value).inputFingerprint, 'string');
    }
    await f.page.locator('#signout').click();
    const watermark = f.api.requests.length;
    await f.signin('bob');
    assert.equal(await f.page.locator('#history li').count(), 0);
    assert.equal(await f.page.locator('#detail').isVisible(), false);
    assert.equal(f.api.requests.slice(watermark).filter(req => req.owner === 'bob' && req.path.includes(id)).length, 0);
    assert.deepEqual(f.errors, []);
  });

  test(`${kind} starter UI streams progress and downloads success independently of delivery failure`, async t => {
    const f = await browserStarter(t, template); await f.signin(); await f.start();
    await f.page.locator('#detail').waitFor({state: 'visible'});
    const id = [...f.api.operations.keys()][0];
    f.api.update(id, {progress: {stage: 'generating', completed: 2, total: 3}});
    await f.page.waitForFunction(() => document.getElementById('progress').value === 2);
    assert.ok(f.api.streams > 0);
    f.api.succeeded(id);
    await f.page.waitForFunction(() => !document.getElementById('download').disabled);
    assert.match(await f.page.locator('#business').innerText(), /succeeded/);
    assert.match(await f.page.locator('#delivery').innerText(), /failed/);
    const download = f.page.waitForEvent('download'); await f.page.locator('#download').click();
    const file = await download;
    assert.equal(file.suggestedFilename(), 'export.csv');
    assert.equal(readFileSync(await file.path(), 'utf8'), csv);
    assert.equal(f.api.downloads, 1); assert.equal(f.api.submissions.length, 1);
    f.api.update(id, {completion_delivery: {state: 'succeeded', attempts: 2}});
    await f.page.waitForFunction(() => document.getElementById('delivery').textContent.includes('succeeded'));
    assert.equal(f.api.submissions.length, 1); assert.deepEqual(f.errors, []);
  });

  test(`${kind} starter UI keeps cancelled work private and requests reconciliation`, async t => {
    const f = await browserStarter(t, template); await f.signin(); await f.start();
    await f.page.locator('#detail').waitFor({state: 'visible'});
    const id = [...f.api.operations.keys()][0];
    f.api.update(id, {progress: {stage: 'generating', completed: 2, total: 3}});
    await f.page.waitForFunction(() => document.getElementById('progress').value === 2);
    await f.page.locator('#cancel').click();
    await f.page.locator('#reconciliation').waitFor({state: 'visible'});
    assert.equal(await f.page.locator('#download').isDisabled(), true);
    assert.equal(await f.page.locator('#cancel').isDisabled(), true);
    assert.equal(f.api.submissions.length, 1); assert.equal(f.api.downloads, 0);
    assert.equal(f.api.requests.filter(req => req.path.endsWith('/cancel')).length, 1);
    assert.equal(f.api.requests.filter(req => /recover|retry|resume/.test(req.path)).length, 0);
    assert.equal(f.api.operations.get(id).operation.state, 'requires_reconciliation');
    assert.deepEqual(f.errors, []);
  });
}
