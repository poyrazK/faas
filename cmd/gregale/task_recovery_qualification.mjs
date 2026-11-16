// Owner-only read checks. No hook, migration, traffic write or worker retirement.
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { createRequire } from 'node:module';
let pool;
try {
 const base = pathToFileURL(resolve('package.json'));
 const require = createRequire(base);
 const pg = require('pg');
 const { resolveMcpTaskSettings } = await import(pathToFileURL(resolve('task-runtime.js')));
 const { mcpTaskHandlers } = await import(pathToFileURL(resolve('tasks.js')));
 const { restoreMcpTasks } = await import(pathToFileURL(resolve('task-release.js')));
 const config = JSON.parse(readFileSync('gregale-mcp.json','utf8'));
 const settings = resolveMcpTaskSettings(config,{role:'observer'});
 pool = new pg.Pool({connectionString:settings.databaseURL,max:2,connectionTimeoutMillis:5000,statement_timeout:10000});
 const report = await restoreMcpTasks({pool,namespace:settings.namespace,handlers:mcpTaskHandlers,restore:async()=>{}});
 if (!report.ok) throw new Error('Compatibility unavailable');
 await pool.end(); pool = undefined;
 console.log('{"qualified":true}');
} catch { process.exitCode=1; }
finally { await pool?.end().catch(()=>{}); }
