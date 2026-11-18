import {readFile} from 'node:fs/promises';
import {resolve} from 'node:path';
import {pathToFileURL} from 'node:url';
import {applicationChecks} from './checks.mjs';

const moduleURL = process.env.GREGALE_NODE_SDK ? pathToFileURL(resolve(process.env.GREGALE_NODE_SDK)) : new URL('../../sdk/node/dist/index.js', import.meta.url);
const {createClient,parseProjectVerificationTarget} = await import(moduleURL.href);
const state = resolve(process.env.GREGALE_PROJECT_STATE ?? new URL('.state',import.meta.url).pathname);
const target = parseProjectVerificationTarget(JSON.parse(await readFile(resolve(state,'verification-target.json'),'utf8')));
const client = createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN});
const report = await client.projects.verifyApplication(target,{
  timeoutMs:120000,maxPolls:30,pollDelayMs:2000,
  checks:applicationChecks(target,process.env.GREGALE_APPLICATION_URL),
});
console.log(JSON.stringify(report));
if (report.outcome !== 'verified') process.exitCode = 1;
