import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';

test('bundled bridge contains runnable modules and only explicit deployment assets',async(t)=>{
 const result=await promisify(execFile)(process.execPath,[fileURLToPath(new URL('bundle.mjs',import.meta.url))]);
 const target=result.stdout.trim();assert.ok(target.startsWith(join(tmpdir(),'gregale-issues-slack-')));
 t.after(()=>rm(target,{recursive:true,force:true}));
 assert.deepEqual((await readdir(target)).sort(),['gregale-sdk','package-lock.json','package.json','receipts.mjs','schema.sql','sdk.mjs','server.mjs','setup.mjs']);
 const manifest=JSON.parse(await readFile(join(target,'package.json'),'utf8'));assert.deepEqual(Object.keys(manifest.scripts).sort(),['setup','start']);assert.equal(manifest.dependencies.pg,'8.16.3');
 const sdk=await import(pathToFileURL(join(target,'sdk.mjs')).href);
 assert.equal(sdk.formatIssueSlackMessage({type:'deployment.live'},{appID:'11111111-1111-4111-8111-111111111111',appSlug:'payments',dashboardOrigin:'https://gregale.dev'}),null);
 assert.equal(typeof sdk.issueSlackMessageFromWebhook,'function');
});
