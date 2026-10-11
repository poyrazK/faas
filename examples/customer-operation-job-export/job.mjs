import {fileURLToPath} from 'node:url';
import {runJobOperation} from '@gregale/sdk-node/job-operations-runtime';
import {generateExport} from './export.mjs';

export function runExportJob({apiURL, env = process.env, fetchImpl = fetch}) {
  return runJobOperation({apiURL, env, fetch: fetchImpl}, generateExport);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    await runExportJob({apiURL: process.env.GREGALE_API_URL});
  } catch {
    console.error('Export execution did not confirm completion. Inspect the operation before recovery.');
    process.exitCode = 1;
  }
}
