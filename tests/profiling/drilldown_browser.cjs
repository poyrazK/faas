// Requires playwright-core and Chromium. Run from the repository root:
// NODE_PATH=/path/to/node_modules node tests/profiling/drilldown_browser.cjs
// Set GREGALE_TEST_CHROMIUM or GREGALE_PROFILE_SCREENSHOT when needed.
'use strict';
const assert = require('node:assert/strict');
const http = require('node:http');
const {spawnSync} = require('node:child_process');
const {chromium} = require('playwright-core');

(async () => {
  const rendered = spawnSync('go', ['run', 'tests/profiling/_fixtures/render.go'], {encoding:'utf8', maxBuffer:1024*1024});
  assert.equal(rendered.status, 0, rendered.stderr);
  const missing = spawnSync('go', ['run', 'tests/profiling/_fixtures/render.go'], {encoding:'utf8', maxBuffer:1024*1024,env:{...process.env,GREGALE_PROFILE_FIXTURE:'missing'}});
  assert.equal(missing.status,0,missing.stderr);
  const server = http.createServer((req,res) => {
    res.setHeader('Content-Type','text/html; charset=utf-8');
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'nonce-fixture-nonce'; style-src 'nonce-fixture-nonce'");
    res.end(req.url.startsWith('/missing')?missing.stdout:rendered.stdout);
  });
  await new Promise(resolve => server.listen(0,'127.0.0.1',resolve));
  let browser;
  try {
    browser = await chromium.launch({executablePath:process.env.GREGALE_TEST_CHROMIUM || '/usr/bin/chromium', args:['--no-sandbox']});
    const page = await browser.newPage({viewport:{width:1280,height:1500}});
    const failures=[];
    page.on('pageerror', err => failures.push(err.message));
    page.on('console', msg => {if(msg.type()==='error') failures.push(msg.text())});
    await page.goto(`http://127.0.0.1:${server.address().port}/dashboard/apps/profile-demo/profiles`);
    assert.equal(await page.locator('#cpu-chart a').count(),60);
    assert.match(await page.locator('section[aria-label="Collection coverage"]').innerText(),/600\.000 of 3600\.000/);
    const candidateSource='https://github.com/acme/profile-demo/blob/'+'a'.repeat(40)+'/apps/api/app.js#L24';
    const baselineSource='https://github.com/acme/profile-demo/blob/'+'b'.repeat(40)+'/apps/api/app.js#L18';
    assert.equal(await page.locator('.function-source').first().getAttribute('href'),candidateSource);
    assert.equal(await page.locator('.candidate-source').first().getAttribute('href'),candidateSource);
    assert.equal(await page.locator('.baseline-source').first().getAttribute('href'),baselineSource);
    assert.equal(await page.locator('#diff-flamegraph rect[data-change="increase"]').count(),1);
    assert.equal(await page.locator('#diff-flamegraph rect[data-change="decrease"]').count(),1);
    assert.equal(await page.locator('#diff-flamegraph rect[data-change="unknown"]').count(),2);
    assert.match(await page.locator('[aria-label="Comparison collection coverage"]').innerText(),/Baseline collection: 600\.000 of 3600\.000.*Candidate collection: 600\.000 of 3600\.000/);
    await page.locator('#diff-search').fill('parseJSON');
    assert.equal(await page.locator('#diff-flamegraph rect[data-search-match="true"][data-change="increase"]').count(),1);
    await page.locator('#diff-flamegraph g').filter({hasText:'parseJSON'}).focus();
    await page.keyboard.press('Enter');
    assert.equal(await page.locator('#diff-flamegraph g').count(),1);
    assert.match(await page.locator('#diff-frame-details').innerText(),/all → handler → parseJSON/);
    assert.match(await page.locator('#diff-frame-details').innerText(),/0\.001111 CPU\/s.*0\.002333 CPU\/s.*\+0\.001222 CPU\/s/s);
    assert.equal(await page.locator('.diff-baseline-source').getAttribute('href'),baselineSource);
    assert.equal(await page.locator('.diff-candidate-source').getAttribute('href'),candidateSource);
    await page.locator('#diff-reset').click();
    assert.equal(await page.locator('#diff-flamegraph g').count(),6);
    await page.locator('#diff-search').fill('');
    if(process.env.GREGALE_PROFILE_SCREENSHOT) await page.screenshot({path:process.env.GREGALE_PROFILE_SCREENSHOT,fullPage:true});
    await page.locator('#diff-flamegraph g').filter({hasText:'newPath'}).click();
    assert.match(await page.locator('#diff-frame-details').innerText(),/Baseline inclusive CPU\/s\s*Not observed.*Delta CPU\/s\s*Unknown/s);
    assert.equal(await page.locator('.diff-baseline-source').count(),0);
    await page.locator('#diff-reset').click();
    await page.locator('#search').fill('parseJSON');
    assert.equal(await page.locator('#flamegraph rect[fill="#fbbf24"]').count(),1);
    await page.locator('#flamegraph g').nth(2).focus();
    await page.keyboard.press('Enter');
    assert.equal(await page.locator('#frame-source a').getAttribute('href'),candidateSource);
    assert.equal(await page.locator('#frame-source a').getAttribute('rel'),'noopener noreferrer');
    let openedURL;
    await page.context().route('https://github.com/**',route=>{openedURL=route.request().url();return route.fulfill({body:'Commit-pinned source fixture'})});
    const popupPromise=page.waitForEvent('popup');
    await page.locator('#frame-source a').click();
    const popup=await popupPromise;
    await popup.waitForLoadState();
    assert.equal(popup.url(),candidateSource);
    assert.equal(openedURL,candidateSource.split('#')[0]);
    await popup.close();
    await page.locator('#reset').click();
    await page.locator('#flamegraph g').nth(1).click();
    assert.equal(await page.locator('#flamegraph g').count(),4);
    await page.locator('#reset').click();
    assert.equal(await page.locator('#flamegraph g').count(),5);
    const bar = page.locator('#cpu-chart a').nth(28);
    const before = new URL(await bar.getAttribute('href'), page.url());
    await page.locator('select[name="deployment_id"]').selectOption('22222222-2222-4222-8222-222222222222');
    await page.locator('#profile-query input[name="runtime"]').fill('python313');
    const selected = new URL(await bar.getAttribute('href'), page.url());
    assert.equal(selected.searchParams.get('deployment_id'),'22222222-2222-4222-8222-222222222222');
    assert.equal(selected.searchParams.get('runtime'),'python313');
    for(const field of ['start','end','chart_start','chart_end']) assert.equal(selected.searchParams.get(field),before.searchParams.get(field));
    await bar.click();
    await page.waitForURL(url => url.searchParams.get('runtime')==='python313');
    assert.equal(new URL(page.url()).searchParams.get('deployment_id'),'22222222-2222-4222-8222-222222222222');
    await page.goto(`http://127.0.0.1:${server.address().port}/missing`);
    assert.equal(await page.locator('#diff-flamegraph').count(),0);
    assert.match(await page.locator('body').innerText(),/Both deployments need CPU samples before comparison/);
    assert.deepEqual(failures,[],'Browser script or CSP errors');
    console.log('Differential colors, rates, zoom/search, revision-specific source links, unobserved paths, absent profiles, coverage, CPU drill-down, keyboard access and CSP: PASS');
  } finally {
    if(browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
})().catch(err => {console.error(err);process.exitCode=1});
