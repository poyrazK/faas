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
    const initial = await page.goto(fixture.url + '?template=scheduled-report');
    assert.equal(initial.status(), 200, 'starter page must load');
    await page.locator('#automation-name').fill('browser-report');
    const definition = JSON.parse(await page.locator('#automation-definition').inputValue());
    definition.name = 'browser-report';
    await page.locator('#automation-definition').fill(JSON.stringify(definition, null, 2));
    const sample = await page.locator('#automation-sample').inputValue();
    const scenarios = await page.locator('#automation-scenarios').inputValue();
    async function submit(label, marker) {
      await Promise.all([page.waitForNavigation(), page.getByRole('button', { name: label, exact: true }).click()]);
      assert.ok(await page.getByText(marker, { exact: false }).count(), 'missing feedback for ' + label);
      assert.equal(await page.locator('[role=alert]').count(), 0, 'action returned an error for ' + label);
    }
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
