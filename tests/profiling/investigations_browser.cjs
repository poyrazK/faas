'use strict';
const assert = require('node:assert/strict');
const http = require('node:http');
const {spawnSync} = require('node:child_process');
const {chromium} = require('playwright-core');

(async () => {
  const fixtures = {};
  for (const mode of ['', 'saved', 'candidate-saved', 'expired', 'unobserved']) {
    const result = spawnSync('go', ['run', 'tests/profiling/_fixtures/render.go'], {
      encoding: 'utf8', maxBuffer: 1024 * 1024, env: {...process.env, GREGALE_PROFILE_FIXTURE: mode},
    });
    assert.equal(result.status, 0, result.stderr);
    fixtures[mode] = result.stdout;
  }
  const submitted = [];
  const server = http.createServer((req, res) => {
    if (req.method === 'POST') {
      let body = '';
      req.on('data', part => {body += part});
      req.on('end', () => {
        submitted.push({path: req.url, values: new URLSearchParams(body)});
        res.writeHead(303, {Location: '/saved'}); res.end();
      });
      return;
    }
    const mode = new URL(req.url, 'http://localhost').pathname.slice(1);
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'nonce-fixture-nonce'; style-src 'nonce-fixture-nonce'");
    res.end(fixtures[mode] ?? fixtures['']);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    browser = await chromium.launch({executablePath: process.env.GREGALE_TEST_CHROMIUM || '/usr/bin/chromium', args: ['--no-sandbox']});
    const page = await browser.newPage({viewport: {width: 1280, height: 1500}});
    const errors = [];
    page.on('pageerror', err => errors.push(err.message));
    page.on('console', msg => {if (msg.type() === 'error') errors.push(msg.text())});
    const base = `http://127.0.0.1:${server.address().port}`;
    await page.goto(base);
    await page.locator('#diff-flamegraph g').filter({hasText: 'parseJSON'}).click();
    await page.locator('#investigation-save input[name="title"]').fill('JSON parsing regression');
    await page.locator('#investigation-save textarea[name="findings"]').fill('parseJSON increased');
    await page.locator('#investigation-save textarea[name="notes"]').fill('Compare equivalent traffic before rollout');
    await page.locator('#investigation-save button[value="save"]').click();
    await page.waitForURL(base + '/saved');
    const created = submitted[0];
    assert.equal(created.path, '/dashboard/apps/profile-demo/profiles/investigations');
    assert.equal(created.values.get('csrf_token'), 'fixture-csrf');
    assert.equal(created.values.get('expected_revision'), '0');
    assert.equal(created.values.get('title'), 'JSON parsing regression');
    assert.equal(created.values.get('baseline_id'), '22222222-2222-4222-8222-222222222222');
    assert.equal(created.values.get('deployment_id'), '11111111-1111-4111-8111-111111111111');
    const path = JSON.parse(created.values.get('selected_path'));
    assert.equal(path.view, 'comparison');
    assert.deepEqual(path.frames.map(frame => frame.name), ['all', 'handler', 'parseJSON']);
    assert.equal(path.frames[2].file, 'app.js');
    assert.match(await page.locator('#investigation-path-status').innerText(), /Restored comparison call path: all → handler → parseJSON/);
    assert.equal(await page.locator('#diff-flamegraph g').count(), 1);
    assert.match(await page.locator('.diff-baseline-source').getAttribute('href'), /b{40}.*#L18$/);
    assert.match(await page.locator('.diff-candidate-source').getAttribute('href'), /a{40}.*#L24$/);
    const share = new URL(await page.locator('#investigation-share').getAttribute('href'), base);
    assert.equal(share.searchParams.get('investigation_id'), '33333333-3333-4333-8333-333333333333');
    assert.equal(share.searchParams.size, 1);
    await page.locator('#investigation-save textarea[name="notes"]').fill('Team updated notes');
    await page.locator('#investigation-save button[value="save"]').click();
    await page.waitForLoadState();
    assert.equal(submitted[1].values.get('expected_revision'), '3');
    assert.equal(submitted[1].values.get('notes'), 'Team updated notes');
    if (process.env.GREGALE_PROFILE_INVESTIGATION_SCREENSHOT) await page.screenshot({path: process.env.GREGALE_PROFILE_INVESTIGATION_SCREENSHOT, fullPage: true});
    await page.goto(base + '/candidate-saved');
    assert.equal(await page.locator('#flamegraph g').count(), 1);
    assert.match(await page.locator('#investigation-path-status').innerText(), /Restored candidate call path/);
    await page.goto(base + '/unobserved');
    assert.match(await page.locator('#investigation-path-status').innerText(), /not observed.*saved selection is preserved/);
    assert.equal(JSON.parse(await page.locator('#investigation-save input[name="selected_path"]').inputValue()).frames[2].name, 'removedFunction');
    await page.goto(base + '/expired');
    assert.equal(await page.locator('#diff-flamegraph').count(), 0);
    assert.equal(await page.locator('#flamegraph').count(), 0);
    assert.match(await page.locator('body').innerText(), /profile window has expired/);
    assert.equal(await page.locator('#investigation-save textarea[name="notes"]').inputValue(), 'Compare equivalent traffic before rollout');
    assert.match(await page.locator('#investigation-path-status').innerText(), /profile data is unavailable.*saved selection is preserved/);
    assert.equal(JSON.parse(await page.locator('#investigation-save input[name="selected_path"]').inputValue()).frames[2].name, 'parseJSON');
    if (process.env.GREGALE_PROFILE_INVESTIGATION_SCREENSHOT) await page.screenshot({path: process.env.GREGALE_PROFILE_INVESTIGATION_SCREENSHOT.replace(/\.png$/, '-expired.png'), fullPage: true});
    assert.deepEqual(errors, []);
    console.log('Investigation creation, revisioned edits, authenticated link, complete call-path restoration, missing paths, expiry, notes and CSP: PASS');
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(err => {console.error(err); process.exitCode = 1});
