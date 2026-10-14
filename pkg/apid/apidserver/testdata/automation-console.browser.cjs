const fs = require('node:fs');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');

(async () => {
  const fixture = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const browser = await chromium.launch({ executablePath: process.env.GREGALE_CHROMIUM_PATH || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
    await context.addCookies([{ name: fixture.cookie_name, value: fixture.cookie_value, url: new URL(fixture.url).origin }]);
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    async function submit(label, marker) {
      await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: label, exact: true }).click()]);
      assert.ok(await page.getByText(marker, { exact: false }).count(), 'missing feedback for ' + label);
      assert.equal(await page.locator('[role=alert]').count(), 0, 'action returned an error for ' + label);
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
