import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const moduleURL = process.env.GREGALE_NODE_SDK
  ? pathToFileURL(resolve(process.env.GREGALE_NODE_SDK))
  : new URL('../../sdk/node/dist/index.js', import.meta.url);
const { createClient, parseReviewedProjectPlan } = await import(moduleURL.href);
const mode = process.argv[2] ?? 'preview';
if (!['preview', 'apply', 'resume'].includes(mode)) throw new Error('Use preview, apply or resume');
const projectSlug = process.env.GREGALE_PROJECT_SLUG;
if (!projectSlug || !process.env.FAAS_API || !process.env.FAAS_TOKEN) throw new Error('Set GREGALE_PROJECT_SLUG, FAAS_API and FAAS_TOKEN');
const directory = process.env.GREGALE_PROJECT_SOURCE ?? fileURLToPath(new URL('source', import.meta.url));
const state = resolve(process.env.GREGALE_PROJECT_STATE ?? fileURLToPath(new URL('.state', import.meta.url)));
await mkdir(state, { recursive: true, mode: 0o700 });
const client = createClient(process.env.FAAS_API, { token: process.env.FAAS_TOKEN });
try {
  if (mode === 'preview') {
    if (existsSync(resolve(state, 'apply.json'))) throw Object.assign(new Error('Choose a new GREGALE_PROJECT_STATE for a new review'), { code: 'existing_application' });
    const plan = await client.projects.previewLocal({ directory, projectSlug, appSlugs: { api: projectSlug+'-api', worker: projectSlug+'-worker' } }, { timeoutMs: 120000 });
    await writeFile(resolve(state, 'plan.json'), JSON.stringify(plan), { mode: 0o600 });
    console.log(JSON.stringify({ project: plan.project_slug, canApply: plan.preview.can_apply, changes: plan.preview.will_deploy, managed: plan.preview.managed, warnings: plan.preview.warnings }));
  } else {
    const plan = parseReviewedProjectPlan(JSON.parse(await readFile(resolve(state, 'plan.json'), 'utf8')));
    const options = { statePath: resolve(state, 'apply.json'), timeoutMs: 1200000 };
    const result = await (mode === 'apply' ? client.projects.applyPlan(plan, options) : client.projects.resumePlanApplication(plan, options));
    console.log(JSON.stringify({ outcome: result.outcome, attemptId: result.attemptId, workloads: result.workloads }));
    if (result.outcome !== 'succeeded') process.exitCode = 1;
  }
} catch (error) {
  console.error(JSON.stringify({ code: error.code ?? 'request_failed', phase: error.phase, attemptId: error.state?.attempt_id }));
  process.exitCode = 1;
}
