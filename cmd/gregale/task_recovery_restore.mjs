// The trusted CLI supplies the fixed hook. Hold the Task release advisory lock
// across retained compatibility/admission checks and the actual native restore.
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { createRequire } from 'node:module';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
let pool;
try {
 const input=JSON.parse(process.argv[1]);
 if (!Array.isArray(input.command)||!input.command.length||input.command.some(arg=>typeof arg!=='string'||!arg||arg.includes('\0'))||!Number.isSafeInteger(input.timeoutMs)||input.timeoutMs<1||!Number.isSafeInteger(input.maxBufferBytes)||input.maxBufferBytes<1) throw new Error('Invalid restore hook');
 const require=createRequire(pathToFileURL(resolve('package.json')));
 const pg=require('pg');
 const {resolveMcpTaskSettings}=await import(pathToFileURL(resolve('task-runtime.js')));
 const {mcpTaskHandlers}=await import(pathToFileURL(resolve('tasks.js')));
 const {restoreMcpTasks}=await import(pathToFileURL(resolve('task-release.js')));
 const config=JSON.parse(readFileSync('gregale-mcp.json','utf8'));
 const settings=resolveMcpTaskSettings(config,{role:'observer'});
 pool=new pg.Pool({connectionString:settings.databaseURL,max:2,connectionTimeoutMillis:5000,statement_timeout:10000});
 const env={...process.env};delete env.MCP_TASK_MIGRATION_DATABASE_URL;
 const report=await restoreMcpTasks({pool,namespace:settings.namespace,handlers:mcpTaskHandlers,
  restore:()=>promisify(execFile)(input.command[0],input.command.slice(1),{env,timeout:input.timeoutMs,killSignal:'SIGKILL',maxBuffer:input.maxBufferBytes}),
 });
 if (!report.ok) throw new Error('Restore blocked');
 await pool.end();pool=undefined;
 console.log('{"restored":true}');
} catch { process.exitCode=1; }
finally {await pool?.end().catch(()=>{});}
