'use strict';
const assert = require('node:assert/strict');
const http = require('node:http');
const {spawnSync} = require('node:child_process');
const {chromium} = require('playwright-core');

(async () => {
  const fixtures = {};
  for (const mode of ['saved', 'checked', 'stale', 'checked-expired']) {
    const result = spawnSync('go', ['run', 'tests/profiling/_fixtures/render.go'], {
      encoding: 'utf8', maxBuffer: 1024 * 1024, env: {...process.env, GREGALE_PROFILE_FIXTURE: mode},
    });
    assert.equal(result.status, 0, result.stderr);
    fixtures[mode] = result.stdout;
  }
  let submitted;
  const server = http.createServer((req, res) => {
    if (req.method === 'POST') {
      let body = '';
      req.on('data', part => {body += part});
      req.on('end', () => {
        submitted = {path: req.url, values: new URLSearchParams(body)};
        res.writeHead(303, {Location: '/checked'}); res.end();
      });
      return;
    }
    const mode = new URL(req.url, 'http://localhost').pathname.slice(1);
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'nonce-fixture-nonce'; style-src 'nonce-fixture-nonce'");
    res.end(fixtures[mode] ?? fixtures.saved);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    browser = await chromium.launch({executablePath: '/usr/bin/chromium', args: ['--no-sandbox']});
    const page = await browser.newPage({viewport: {width: 1280, height: 1500}});
    const errors = [];
    page.on('pageerror', err => errors.push(err.message));
    page.on('console', msg => {if (msg.type() === 'error') errors.push(msg.text())});
    const base = `http://127.0.0.1:${server.address().port}`;
    await page.goto(base+'/saved');
    const form = page.locator('#regression-check');
    assert.equal(await form.locator('[name="relative_increase_percent"]').inputValue(), '20');
    assert.equal(await form.locator('[name="minimum_coverage_ratio"]').inputValue(), '0.8');
    await form.locator('[name="absolute_increase_cpu_per_second"]').fill('0.001');
    await Promise.all([page.waitForURL('**/checked'), form.getByRole('button', {name: 'Check for regression'}).click()]);
    assert.equal(submitted.path, '/dashboard/apps/profile-demo/profiles/investigations/33333333-3333-4333-8333-333333333333/check');
    assert.equal(submitted.values.get('csrf_token'), 'fixture-csrf');
    assert.equal(submitted.values.get('expected_revision'), '3');
    assert.equal(submitted.values.get('absolute_increase_cpu_per_second'), '0.001');
    assert.match(await page.locator('#regression-status').innerText(), /regressed/);
    assert.match(await page.locator('#regression-evidence').innerText(), /parseJSON/);
    assert.match(await page.locator('#regression-evidence').innerText(), /handler/);
    assert.match(await page.locator('#regression-comparison').getAttribute('href'), /investigation_id=.*#diff-flamegraph$/);
    assert.equal(await page.locator('#regression-stale').count(), 0);
    assert.equal(await page.locator('#regression-check [name="expected_revision"]').inputValue(), '4');
    await page.screenshot({path: '/tmp/gregale-regression-assessment.png', fullPage: true});
    await page.goto(base+'/stale');
    assert.match(await page.locator('#regression-stale').innerText(), /investigation has changed/);
    assert.equal(await page.locator('#regression-check [name="expected_revision"]').inputValue(), '5');
    await page.goto(base+'/checked-expired');
    assert.match(await page.locator('section[aria-label="Profiling regression assessment"]').innerText(), /Historical assessment/);
    assert.match(await page.locator('#regression-status').innerText(), /regressed/);
    assert.equal(await page.locator('#diff-flamegraph').count(), 0);
    assert.equal(await page.locator('#regression-check [name="expected_revision"]').inputValue(), '4');
    assert.deepEqual(errors, []);
    console.log('PASS: regression form, stored evidence, comparison link, stale revision and expired historical result');
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(err => {console.error(err); process.exitCode = 1});
