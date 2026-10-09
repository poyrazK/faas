'use strict';
const assert = require('node:assert/strict');
const http = require('node:http');
const {spawnSync} = require('node:child_process');
const {chromium} = require('playwright-core');

(async () => {
  const fixture = spawnSync('go', ['run', 'tests/profiling/_fixtures/render.go'], {
    encoding: 'utf8', maxBuffer: 1024 * 1024, env: {...process.env, GREGALE_PROFILE_FIXTURE: 'automatic'},
  });
  assert.equal(fixture.status, 0, fixture.stderr);
  let submitted;
  const server = http.createServer((req, res) => {
    if (req.method === 'POST') {
      let body = '';
      req.on('data', part => {body += part});
      req.on('end', () => {
        submitted = {path: req.url, values: new URLSearchParams(body)};
        res.writeHead(303, {Location: '/saved-policy'}); res.end();
      });
      return;
    }
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'nonce-fixture-nonce'; style-src 'nonce-fixture-nonce'");
    res.end(fixture.stdout);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    browser = await chromium.launch({executablePath: '/usr/bin/chromium', args: ['--no-sandbox']});
    const page = await browser.newPage({viewport: {width: 1280, height: 1500}});
    const errors = [];
    page.on('pageerror', err => errors.push(err.message));
    page.on('console', msg => {if (msg.type() === 'error') errors.push(msg.text())});
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    const form = page.locator('#automatic-profile-policy');
    assert.equal(await form.locator('[name="enabled"]').isChecked(), true);
    assert.equal(await form.locator('[name="window_seconds"]').inputValue(), '300');
    assert.equal(await form.locator('[name="warmup_seconds"]').inputValue(), '120');
    assert.equal(await form.locator('[name="minimum_coverage_ratio"]').inputValue(), '0.8');
    assert.match(await page.locator('#automatic-profile-results').innerText(), /regressed/);
    assert.match(await page.locator('#automatic-profile-results').innerText(), /staging[\s\S]*queued/);
    assert.match(await page.getByRole('link', {name: 'Open comparison and saved investigation'}).getAttribute('href'), /investigation_id=.*#diff-flamegraph$/);
    await page.locator('#automatic-profile-checks').screenshot({path: '/tmp/gregale-automatic-profile-checks.png'});
    await form.locator('[name="warmup_seconds"]').fill('180');
    await form.locator('[name="enabled"]').uncheck();
    await Promise.all([page.waitForURL('**/saved-policy'), form.getByRole('button', {name: 'Save automatic check policy'}).click()]);
    assert.equal(submitted.path, '/dashboard/apps/profile-demo/profiles/deployment-policy');
    assert.equal(submitted.values.get('csrf_token'), 'fixture-policy-csrf');
    assert.equal(submitted.values.get('expected_revision'), '1');
    assert.equal(submitted.values.get('warmup_seconds'), '180');
    assert.equal(submitted.values.has('enabled'), false);
    assert.deepEqual(errors, []);
    console.log('PASS: automatic policy controls, cancellation form, queued/result history and saved comparison links');
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(err => {console.error(err); process.exitCode = 1});
