const fs = require('node:fs');
const assert = require('node:assert/strict');
const { createHmac } = require('node:crypto');
const { chromium } = require('playwright');

(async () => {
  const fixture = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const browser = await chromium.launch({ executablePath: process.env.GREGALE_CHROMIUM_PATH || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] });
  try {
    // The original live journey qualifies the manual, JavaScript-free fallback.
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 }, javaScriptEnabled: fixture.mode !== 'live' });
    await context.addCookies([{ name: fixture.cookie_name, value: fixture.cookie_value, url: new URL(fixture.url).origin }]);
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    async function submit(label, marker) {
      await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: label, exact: true }).click()]);
      assert.ok(await page.getByText(marker, { exact: false }).count(), 'missing feedback for ' + label + '; alerts: ' + (await page.locator('[role=alert]').allTextContents()).join('; '));
      assert.equal(await page.locator('[role=alert]').count(), 0, 'action returned an error for ' + label);
    }
    if (fixture.mode === 'live-progress') {
      const progressURL = new URL(new URL(fixture.url).pathname + '/live-verifications/' + fixture.run_id + '?name=progress-report', fixture.url).href;
      const requests = [];
      const responses = [];
      page.on('request', request => { if (request.url() === progressURL) requests.push(request); });
      await page.route(fixture.url, async route => {
        const response = await route.fetch();
        const body = (await response.text()).replace(/data-max-requests="[^"]*"/, 'data-max-requests="5"').replace(/data-timeout-ms="[^"]*"/, 'data-timeout-ms="1500"');
        await route.fulfill({ response, body });
      });
      let pendingHTML;
      let firstRead;
      const read = new Promise(resolve => { firstRead = resolve; });
      await page.route(progressURL, async route => {
        const response = await route.fetch();
        const body = await response.text();
        responses.push({method: route.request().method(), status: response.status()});
        const first = route.request().method() === 'GET' && !pendingHTML;
        if (first) pendingHTML = body;
        await route.fulfill({ response, body });
        if (first) firstRead();
      });
      await page.goto(fixture.url);
      await read;
      await page.locator('#automation-sample').fill('PRIVATE unsaved invalid JSON');
      const status = page.locator('[data-live-progress-status]');
      await page.evaluate(() => {
        Object.defineProperty(document, 'visibilityState', {configurable: true, value: 'hidden'});
        document.dispatchEvent(new Event('visibilitychange'));
      });
      assert.ok((await status.innerText()).includes('paused'));
      const hiddenCount = requests.length;
      await page.waitForTimeout(3300);
      assert.equal(requests.length, hiddenCount, 'hidden page kept polling');
      await page.evaluate(() => {
        Object.defineProperty(document, 'visibilityState', {configurable: true, value: 'visible'});
        document.dispatchEvent(new Event('visibilitychange'));
      });
      await page.waitForFunction(() => document.getElementById('automation-live-result').dataset.liveState === 'running');
      await page.waitForFunction(() => document.getElementById('automation-live-result').dataset.liveState === 'awaiting_event');
      try {
        await page.getByText('Live verification passed.', {exact: true}).waitFor();
      } catch (error) {
        throw new Error(error.message + '\nProgress diagnostics: ' + JSON.stringify({status: await status.textContent(), requests: requests.map(r => r.method()), responses, result: await page.locator('#automation-live-result').innerText(), errors}));
      }
      assert.equal(await page.locator('#automation-sample').inputValue(), 'PRIVATE unsaved invalid JSON');
      assert.equal(await page.locator('#automation-live-result [data-live-outdated]').isVisible(), true);
      assert.equal(page.url(), fixture.url);
      assert.equal((await page.locator('#automation-live-result').innerText()).includes('PRIVATE'), false);
      assert.equal(requests.filter(r => r.method() === 'POST').length, 1);
      const posted = new URLSearchParams(requests.find(r => r.method() === 'POST').postData());
      assert.deepEqual([...posted.keys()], ['csrf_token']);
      const completedCount = requests.length;
      await page.waitForTimeout(3300);
      assert.equal(requests.length, completedCount, 'completed run kept polling');
      await page.getByRole('button', {name: 'Check live results', exact: true}).click();
      await page.waitForFunction(() => document.querySelector('[data-live-progress-status]').textContent.includes('checks finished'));
      assert.equal(await page.locator('#automation-sample').inputValue(), 'PRIVATE unsaved invalid JSON');
      assert.equal(requests.filter(r => r.method() === 'POST').length, 1, 'current report was checked again');
      if (process.env.GREGALE_AUTOMATION_PROGRESS_SCREENSHOT) {
        await page.locator('#automation-live-verification').screenshot({path: process.env.GREGALE_AUTOMATION_PROGRESS_SCREENSHOT});
      }
      await page.unroute(progressURL);
      await page.route(progressURL, route => route.fulfill({status: 401, contentType: 'application/problem+json', body: '{}'}));
      await page.getByRole('button', {name: 'Resume automatic updates', exact: true}).click();
      await page.waitForFunction(() => document.querySelector('[data-live-progress-status]').textContent.includes('authentication'));
      const deniedCount = requests.length;
      await page.waitForTimeout(3300);
      assert.equal(requests.length, deniedCount, 'authentication failure kept retrying');
      await page.unroute(progressURL);
      await page.route(progressURL, async route => {
        await new Promise(resolve => setTimeout(resolve, 2300));
        try { await route.abort(); } catch { /* The request timeout already aborted it. */ }
      });
      await page.getByRole('button', {name: 'Resume automatic updates', exact: true}).click();
      await page.waitForFunction(() => document.querySelector('[data-live-progress-status]').textContent.includes('timed out'));
      await page.unrouteAll({behavior: 'wait'});
      await page.route(fixture.url, async route => {
        const response = await route.fetch();
        const body = (await response.text()).replace(/data-max-requests="[^"]*"/, 'data-max-requests="3"').replace(/data-interval-ms="[^"]*"/, 'data-interval-ms="50"');
        await route.fulfill({response, body});
      });
      await page.route(progressURL, route => route.fulfill({status: 200, contentType: 'text/html', body: pendingHTML}));
      await page.goto(fixture.url);
      const beforeBudget = requests.length;
      await page.getByRole('button', {name: 'Resume automatic updates', exact: true}).click();
      await page.waitForFunction(() => document.querySelector('[data-live-progress-status]').textContent.includes('request limit'));
      assert.equal(requests.length - beforeBudget, 3, 'request budget was not enforced');
      await page.waitForTimeout(200);
      assert.equal(requests.length - beforeBudget, 3, 'exhausted polling budget kept retrying');
      await page.unroute(progressURL);
      await page.route(progressURL, route => route.fulfill({status: 200, contentType: 'text/html', body: '<p>Invalid result fragment</p>'}));
      await page.getByRole('button', {name: 'Resume automatic updates', exact: true}).click();
      await page.waitForFunction(() => document.querySelector('[data-live-progress-status]').textContent.includes('invalid result'));
      assert.equal(await page.locator('#automation-live-result').getAttribute('data-live-run'), fixture.run_id);
      await page.goto('about:blank');
      const navigatedCount = requests.length;
      await page.waitForTimeout(200);
      assert.equal(requests.length, navigatedCount, 'navigation left a polling loop alive');
      assert.equal(errors.length, 0);
      return;
    }
    if (fixture.mode === 'live') {
      assert.equal((await page.goto(fixture.url + '?template=scheduled-report')).status(), 200);
      await page.locator('#automation-name').fill('browser-live');
      await submit('Add live assertion', 'Test case edits applied locally.');
      await page.locator('#test-live-state').selectOption('succeeded');
      await page.locator('input[name=test_live_assert_output]').check();
      await page.locator('#test-live-output').fill('null');
      await page.locator('#test-live-attempt-count').fill('2');
      await submit('Add live assertion', 'Test case edits applied locally.');
      await page.locator('#test-live-state').selectOption('succeeded');
      await submit('Save draft', 'Draft saved.');
      await submit('Save test data', 'Test data saved.');
      await submit('Check and publish', 'Checks passed and the draft was published.');
      assert.equal(await page.locator('input[name=confirm_live]').isChecked(), false);
      const denied = await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: 'Start live verification', exact: true }).click()]);
      assert.equal(denied[0].status(), 400);
      assert.ok(await page.getByRole('alert').getByText('Confirm that live verification', { exact: false }).count());
      await page.locator('input[name=confirm_live]').check();
      await submit('Start live verification', 'Live verification started.');
      assert.ok(await page.locator('#automation-live-result').getByText('No assertion verdict yet.', { exact: false }).count());
      await submit('Check live results', 'No assertion verdict yet.');
      await submit('Check live results', 'Live verification passed.');
      assert.equal((await page.locator('#automation-live-result').innerText()).includes('PRIVATE'), false);
      if (process.env.GREGALE_AUTOMATION_LIVE_SCREENSHOT) {
        await page.locator('#automation-live-verification').screenshot({ path: process.env.GREGALE_AUTOMATION_LIVE_SCREENSHOT });
      }
      await page.goto(fixture.url + '?name=browser-live&verification=' + await page.locator('input[name=verification_id]').inputValue());
      assert.ok(await page.getByText('Live verification passed.', { exact: true }).count());
      await page.locator('#test-live-run-status').selectOption('failed');
      await page.locator('#test-live-state').selectOption('failed');
      await submit('Save test data', 'Test data saved.');
      assert.ok(await page.getByText('This verification is outdated', { exact: false }).count());
      await submit('Check live results', 'Live verification passed.');
      assert.ok(await page.getByText('This verification is outdated', { exact: false }).count());
      await page.getByText('Remove saved test data', { exact: true }).click();
      await page.locator('input[name=confirm_clear_tests]').check();
      await submit('Clear saved test data', 'Saved test data and results removed.');
      assert.equal(await page.locator('#automation-live-result').count(), 0);
      assert.equal(errors.length, 0);
      return;
    }
    if (fixture.mode === 'live-editor') {
      assert.equal((await page.goto(fixture.url + '?template=scheduled-report')).status(), 200);
      await page.locator('#automation-name').fill('browser-live-editor');
      await submit('Save draft', 'Draft saved.');
      const originalMockState = await page.locator('#test-state').inputValue();
      await submit('Add live assertion', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-live-state').inputValue(), '');
      assert.equal(await page.locator('#test-live-state option[value=mocked]').count(), 0);
      await page.locator('#test-live-state').selectOption('succeeded');
      await page.locator('input[name=test_live_assert_output]').check();
      await page.locator('#test-live-output').fill('{"id":9007199254740993}');
      await page.locator('#test-live-attempt-count').fill('425');
      await submit('Add live assertion', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-live-step').inputValue(), 'deliver');
      await page.locator('#test-live-state').selectOption('skipped');
      await page.locator('input[name=test_live_assert_output]').check();
      await page.locator('#test-live-output').fill('null');
      await page.locator('#test-live-attempt-count').fill('0');
      await submit('Save test data', 'Test data saved.');
      await page.goto(fixture.url + '?name=browser-live-editor');
      assert.match(await page.locator('#test-live-output').inputValue(), /^\{\s*"id"\s*:\s*9007199254740993\s*\}$/);
      assert.equal(await page.locator('#test-live-attempt-count').inputValue(), '425');
      assert.equal(await page.locator('#test-state').inputValue(), originalMockState);
      await page.locator('#test-live-select').selectOption('1');
      await submit('Open live assertion', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-live-output').inputValue(), 'null');
      assert.equal(await page.locator('#test-live-attempt-count').inputValue(), '0');
      await page.locator('#test-live-attempt-count').fill('426');
      const invalid = await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: 'Apply case changes', exact: true }).click()]);
      assert.equal(invalid[0].status(), 400);
      assert.ok(await page.getByRole('alert').getByText('Expected live attempt count', { exact: false }).count());
      assert.equal(await page.locator('#test-live-step').inputValue(), 'deliver');
      assert.equal(await page.locator('#test-live-attempt-count').inputValue(), '426');
      await page.locator('#test-live-attempt-count').fill('');
      await page.locator('input[name=test_live_assert_output]').uncheck();
      await submit('Apply case changes', 'Test case edits applied locally.');
      await page.locator('input[name=test_editor_mode][value=json]').check();
      await submit('Apply editor mode', 'Test case edits applied locally.');
      let raw = await page.locator('#automation-scenarios').inputValue();
      assert.ok(raw.includes('9007199254740993'));
      raw = raw.replace(/"live":\s*\{/, '"live": {"expected_run_status":"failed",');
      await page.locator('#automation-scenarios').fill(raw);
      await page.locator('input[name=test_editor_mode][value=guided]').check();
      await submit('Apply editor mode', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-live-run-status').inputValue(), 'failed');
      assert.match(await page.locator('#test-live-output').inputValue(), /^\{\s*"id"\s*:\s*9007199254740993\s*\}$/);
      await submit('Duplicate case', 'Test case edits applied locally.');
      assert.match(await page.locator('#test-live-output').inputValue(), /^\{\s*"id"\s*:\s*9007199254740993\s*\}$/);
      if (process.env.GREGALE_AUTOMATION_LIVE_EDITOR_SCREENSHOT) {
        await page.locator('#automation-live-editor').screenshot({ path: process.env.GREGALE_AUTOMATION_LIVE_EDITOR_SCREENSHOT });
      }
      await page.locator('input[name=test_live_assert_output]').check();
      await page.locator('#test-live-output').fill('invalid removed output');
      await submit('Remove live assertion', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-live-step').inputValue(), 'deliver');
      assert.equal(await page.locator('input[name=test_live_assert_output]').isChecked(), false);
      assert.equal(await page.locator('#test-live-attempt-count').inputValue(), '');
      await page.locator('#test-live-attempt-count').fill('999');
      await submit('Disable live verification for this case', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-live-step').count(), 0);
      assert.equal(await page.locator('#test-state').inputValue(), originalMockState);
      await submit('Save test data', 'Test data saved.');
      await page.goto(fixture.url + '?name=browser-live-editor');
      await page.locator('#test-case-select').selectOption('1');
      await submit('Open case', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-live-step').count(), 0);
      await page.locator('#test-case-select').selectOption('0');
      await submit('Open case', 'Test case edits applied locally.');
      assert.match(await page.locator('#test-live-output').inputValue(), /^\{\s*"id"\s*:\s*9007199254740993\s*\}$/);
      assert.equal(errors.length, 0);
      return;
    }
    if (fixture.mode === 'case-editor') {
      assert.equal((await page.goto(fixture.url + '?template=scheduled-report')).status(), 200);
      await page.locator('input[name=editor_mode][value=json]').check();
      await page.getByText('Advanced definition (JSON)', { exact: true }).click();
      const definition = JSON.parse(await page.locator('#automation-definition').inputValue());
      definition.name = 'browser-guided-cases';
      await page.locator('#automation-name').fill(definition.name);
      await page.locator('#automation-definition').fill(JSON.stringify(definition));
      await submit('Save draft', 'Draft saved.');
      await submit('Duplicate case', 'Test case edits applied locally.');
      await page.locator('#test-case-name').fill('renamed <case>');
      await page.locator('input[name=test_assert_output]').check();
      await page.locator('#test-output').fill('null');
      await page.locator('#test-attempt-count').fill('25');
      await submit('Apply case changes', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-output').inputValue(), 'null');
      await submit('Save test data', 'Test data saved.');
      await page.goto(fixture.url + '?name=' + definition.name);
      await page.locator('#test-case-select').selectOption('1');
      await submit('Open case', 'Test case edits applied locally.');
      assert.equal(await page.locator('#test-case-name').inputValue(), 'renamed <case>');
      await submit('Run saved test cases', 'Some saved cases failed.');
      assert.ok(await page.getByText('Expected output did not match.', { exact: true }).count());
      assert.ok(await page.getByText('Expected attempt count did not match.', { exact: true }).count());
      if (process.env.GREGALE_AUTOMATION_EDITOR_SCREENSHOT) {
        await page.locator('#automation-test-editor').screenshot({ path: process.env.GREGALE_AUTOMATION_EDITOR_SCREENSHOT });
      }
      await page.locator('input[name=test_assert_output]').uncheck();
      await page.locator('#test-attempt-count').fill('');
      assert.ok((await page.locator('#automation-test-freshness').innerText()).includes('outdated'));
      const denied = await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: 'Run saved test cases', exact: true }).click()]);
      assert.equal(denied[0].status(), 400);
      assert.ok(await page.getByRole('alert').getByText('Save your current sample', { exact: false }).count());
      await submit('Save test data', 'Test data saved.');
      await submit('Run saved test cases', 'All saved cases passed.');
      await submit('Remove case', 'Test case edits applied locally.');
      await submit('Add test case', 'Test case edits applied locally.');
      await page.locator('#test-case-name').fill('new input case');
      await submit('Add assertion', 'Test case edits applied locally.');
      await page.locator('#test-state').selectOption('mocked');
      await submit('Add assertion', 'Test case edits applied locally.');
      await submit('Remove assertion', 'Test case edits applied locally.');
      await submit('Save test data', 'Test data saved.');
      await submit('Run saved test cases', 'All saved cases passed.');
      await submit('Check and publish', 'Checks passed and the draft was published.');
      assert.equal(errors.length, 0);
      return;
    }
    if (fixture.mode === 'cases') {
      const initial = await page.goto(fixture.url + '?template=scheduled-report');
      assert.equal(initial.status(), 200);
      await page.locator('input[name=editor_mode][value=json]').check();
      await page.getByText('Advanced definition (JSON)', { exact: true }).click();
      const definition = JSON.parse(await page.locator('#automation-definition').inputValue());
      definition.name = 'browser-saved-cases';
      await page.locator('#automation-name').fill(definition.name);
      await page.locator('#automation-definition').fill(JSON.stringify(definition, null, 2));
      await submit('Save draft', 'Draft saved.');
      await page.locator('input[name=test_editor_mode][value=json]').check();
      await page.getByText('Advanced test cases (JSON)', { exact: true }).click();
      const sample = await page.locator('#automation-sample').inputValue();
      const cases = JSON.parse(await page.locator('#automation-scenarios').inputValue());
      cases.push(JSON.parse(JSON.stringify(cases[0])));
      cases[0].name = 'passing report';
      cases[1].name = 'delivery output mismatch';
      cases[1].expectations[0].output = { unexpected: true };
      await page.locator('#automation-scenarios').fill(JSON.stringify(cases, null, 2));
      await submit('Save test data', 'Test data saved.');
      await page.goto(fixture.url + '?name=' + definition.name);
      assert.deepEqual(JSON.parse(await page.locator('#automation-sample').inputValue()), JSON.parse(sample));
      assert.deepEqual(JSON.parse(await page.locator('#automation-scenarios').inputValue()), cases);
      await submit('Run saved test cases', 'Some saved cases failed.');
      assert.ok(await page.getByRole('heading', { name: 'passing report: passed', exact: true }).count());
      assert.ok(await page.getByRole('heading', { name: 'delivery output mismatch: failed', exact: true }).count());
      assert.ok(await page.getByText('Results are current', { exact: false }).count());
      if (process.env.GREGALE_AUTOMATION_CASES_SCREENSHOT) {
        await page.locator('#automation-test-report').screenshot({ path: process.env.GREGALE_AUTOMATION_CASES_SCREENSHOT });
      }
      await page.locator('input[name=test_editor_mode][value=json]').check();
      await page.getByText('Advanced test cases (JSON)', { exact: true }).click();
      cases[1].expectations = cases[0].expectations;
      await page.locator('#automation-scenarios').fill(JSON.stringify(cases, null, 2));
      assert.ok((await page.locator('#automation-test-freshness').innerText()).includes('Results are outdated.'));
      await submit('Save test data', 'Results are outdated.');
      await submit('Run saved test cases', 'All saved cases passed.');
      await submit('Check and publish', 'Checks passed and the draft was published.');
      assert.ok(await page.getByText('Results are current', { exact: false }).count());
      await page.goto(fixture.url + '?name=' + definition.name);
      assert.ok(await page.getByText('All saved cases passed.', { exact: false }).count());
      await page.getByText('Remove saved test data', { exact: true }).click();
      await page.locator('input[name=confirm_clear_tests]').check();
      await submit('Clear saved test data', 'Saved test data and results removed.');
      assert.equal(await page.locator('#automation-test-report').count(), 0);
      assert.equal(errors.length, 0);
      return;
    }
    if (fixture.mode === 'recovery') {
      const initial = await page.goto(fixture.url);
      assert.equal(initial.status(), 200, 'recovery page must load');
      assert.ok(await page.getByRole('heading', { name: 'Run diagnosis', exact: true }).count());
      assert.ok(await page.getByRole('heading', { name: 'Recovery preview', exact: true }).count());
      assert.equal(await page.locator('input[name=expected_resume_count]').inputValue(), '0');
      assert.equal(await page.locator('form[aria-label="Resume selected run"] input[name=confirm_resume]').isChecked(), false);
      await page.getByRole('link', { name: 'Inspect attempts for deliver', exact: true }).click();
      assert.ok(await page.getByRole('heading', { name: 'Step attempts: deliver', exact: true }).count());
      assert.ok(await page.locator('#selected-step').getByText('503', { exact: true }).count());
      if (process.env.GREGALE_AUTOMATION_RECOVERY_SCREENSHOT) {
        await page.locator('#selected-run').screenshot({ path: process.env.GREGALE_AUTOMATION_RECOVERY_SCREENSHOT });
      }
      await page.locator('input[name=confirm_resume]').check();
      await submit('Resume failed run', 'Continuation 1');
      assert.ok(await page.getByText('continuations: 1', { exact: false }).count());
      assert.equal(await page.getByRole('button', { name: 'Resume failed run', exact: true }).count(), 0);
      await page.locator('#selected-run summary').click();
      await page.locator('input[name=confirm_cancel]').check();
      await submit('Cancel run', 'Cancellation was requested.');
      assert.equal(await page.getByRole('button', { name: 'Cancel run', exact: true }).count(), 0);
      const text = await page.locator('body').innerText();
      for (const secret of ['PRIVATE-RECOVERY-INPUT', 'PRIVATE-RECOVERY-OUTPUT', 'PRIVATE-RECOVERY-ERROR']) {
        assert.equal(text.includes(secret), false, 'inspector exposed a private value');
      }
      assert.equal(errors.length, 0, 'browser reported a script error');
      return;
    }
    if (fixture.mode === 'webhook') {
      const initial = await page.goto(fixture.url + '?template=webhook-enrichment');
      assert.equal(initial.status(), 200);
      assert.equal(await page.locator('input[name=editor_mode][value=webhook]').isChecked(), true);
      await page.locator('#automation-name').fill('browser-orders');
      await page.locator('#webhook-source').fill('external.orders');
      await page.locator('#webhook-event-type').fill('order.created');
      await page.locator('#webhook-lookup').fill('/orders/lookup');
      await page.locator('#webhook-enrich').fill('/orders/record');
      await submit('Validate definition', 'Definition is valid.');
      await submit('Simulate steps', 'Simulation completed.');
      await submit('Save draft', 'Draft saved.');
      await submit('Check and publish', 'Checks passed and the draft was published.');
      assert.ok(await page.getByRole('button', { name: 'Enable automatic starts', exact: true }).count());
      await page.getByRole('link', { name: 'signed-orders', exact: true }).click();
      assert.equal(await page.locator('#binding-event-type').count(), 1, 'binding form missing at ' + page.url() + '; ' + (await page.locator('[role=alert]').allTextContents()).join('; '));
      await page.locator('#binding-event-type').fill('order.created');
      await page.locator('input[name="confirm_bind-webhook"]').check();
      await submit('Save webhook binding', 'Bound automation: browser-orders');
      await submit('Enable automatic starts', 'Automatic starts: enabled');
      await page.getByRole('link', { name: 'signed-orders', exact: true }).click();
      const body = JSON.stringify({ customer_id: 'cus_1', order_id: 'order_1', private: 'PRIVATE-BROWSER-WEBHOOK-BODY' });
      const stamp = String(Math.floor(Date.now() / 1000));
      const signature = createHmac('sha256', fixture.webhook_secret).update(stamp + '\nbrowser-event\norder.created\n' + body).digest('hex');
      const headers = { 'Content-Type': 'application/json', 'X-Gregale-Event-ID': 'browser-event', 'X-Gregale-Event-Type': 'order.created', 'X-Gregale-Timestamp': stamp, 'X-Gregale-Signature': 'sha256=' + signature };
      const ingress = new URL(new URL(fixture.webhook_url).pathname, fixture.url).href;
      const accepted = await context.request.post(ingress, { data: body, headers });
      assert.equal(accepted.status(), 202);
      const duplicate = await context.request.post(ingress, { data: body, headers });
      assert.equal(duplicate.status(), 202);
      assert.equal((await duplicate.json()).duplicate, true);
      await page.locator('#webhook-event-id').fill('browser-event');
      await submit('Inspect webhook receipt', 'Webhook receipt');
      assert.ok(await page.getByRole('link', { name: 'Inspect webhook run', exact: true }).count());
      const text = await page.locator('body').innerText();
      for (const privateValue of [fixture.webhook_secret, fixture.webhook_url, 'PRIVATE-BROWSER-WEBHOOK-BODY']) {
        assert.equal(text.includes(privateValue), false, 'webhook view exposed private data');
      }
      if (process.env.GREGALE_AUTOMATION_WEBHOOK_SCREENSHOT) {
        await page.locator('#webhook-setup').screenshot({ path: process.env.GREGALE_AUTOMATION_WEBHOOK_SCREENSHOT });
      }
      await page.getByRole('link', { name: 'Inspect webhook run', exact: true }).click();
      assert.ok(await page.getByRole('heading', { name: 'Selected run', exact: true }).count());
      assert.equal(errors.length, 0, 'browser reported a script error');
      return;
    }
    const initial = await page.goto(fixture.url + '?template=scheduled-report');
    assert.equal(initial.status(), 200, 'starter page must load');
    await page.locator('#automation-name').fill('browser-report');
    if (fixture.mode === 'report') {
      assert.equal(await page.locator('input[name=editor_mode][value=report]').isChecked(), true);
      await page.locator('#report-schedule').fill('30 8 * * 1-5');
      await page.locator('#report-timezone').fill('Europe/Istanbul');
      await page.locator('#report-collect').fill('/reports/collect');
      await page.locator('#report-deliver').fill('/reports/deliver');
      await page.locator('#report-catch-up').selectOption('latest');
      await page.locator('#report-window').fill('2h');
      await submit('Preview edited schedule', 'Edited definition preview');
      assert.ok(await page.locator('#schedule-preview').getByText(/\+03:00/).count());
      assert.ok(await page.locator('#schedule-preview').getByText('Automatic starts in this definition: paused', { exact: false }).count());
      await submit('Save draft', 'Draft saved.');
      const configured = JSON.parse(await page.locator('#automation-definition').inputValue());
      assert.equal(configured.name, 'browser-report');
      assert.equal(configured.trigger.schedule, '30 8 * * 1-5');
      assert.equal(configured.trigger.timezone, 'Europe/Istanbul');
      assert.equal(configured.steps[0].path, '/reports/collect');
      assert.equal(configured.steps[1].path, '/reports/deliver');
      await submit('Check and publish', 'Checks passed and the draft was published.');
      assert.ok(await page.getByRole('heading', { name: 'Published schedule preview', exact: true }).count());
      assert.ok(await page.getByRole('button', { name: 'Enable automatic starts', exact: true }).count());
      if (process.env.GREGALE_AUTOMATION_REPORT_SCREENSHOT) {
        await page.screenshot({ path: process.env.GREGALE_AUTOMATION_REPORT_SCREENSHOT, fullPage: true });
      }
      await submit('Enable automatic starts', 'Automatic starts: enabled');
      assert.ok(await page.getByRole('button', { name: 'Pause automatic starts', exact: true }).count());
      assert.equal(errors.length, 0, 'browser reported a script error');
      return;
    }
    await page.locator('input[name=editor_mode][value=json]').check();
    await page.getByText('Advanced definition (JSON)', { exact: true }).click();
    const definition = JSON.parse(await page.locator('#automation-definition').inputValue());
    definition.name = 'browser-report';
    await page.locator('#automation-definition').fill(JSON.stringify(definition, null, 2));
    const sample = await page.locator('#automation-sample').inputValue();
    const scenarios = await page.locator('#automation-scenarios').inputValue();
    await submit('Validate definition', 'Definition is valid.');
    await submit('Simulate steps', 'Simulation completed.');
    await submit('Save draft', 'Draft saved.');
    assert.equal(await page.locator('#automation-sample').inputValue(), sample, 'save lost the configured input');
    assert.equal(await page.locator('#automation-scenarios').inputValue(), scenarios, 'save lost the configured checks');
    await submit('Check and publish', 'Checks passed and the draft was published.');
    assert.equal(await page.locator('#automation-sample').inputValue(), sample, 'publish lost the configured input');
    if (process.env.GREGALE_AUTOMATION_SCREENSHOT) {
      await page.screenshot({ path: process.env.GREGALE_AUTOMATION_SCREENSHOT, fullPage: true });
    }
    await page.locator('input[name=confirm_run]').check();
    await submit('Run published automation', 'Selected run');
    assert.ok(new URL(page.url()).searchParams.get('run'), 'accepted run has no inspection link');
    await page.getByRole('link', { name: 'Refresh run status', exact: true }).click();
    assert.ok(await page.getByRole('heading', { name: 'Selected run', exact: true }).count(), 'refresh lost the selected run');
    assert.equal(errors.length, 0, 'browser reported a script error');
  } finally {
    await browser.close();
  }
})().catch(error => { process.stderr.write(error.message + '\n'); process.exitCode = 1; });
