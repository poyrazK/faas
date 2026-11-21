import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { agentSchema } from '../../../cmd/gregale/templates/agent-stateless-node/schema.js';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const destination = process.argv[2];
const app = process.argv[3] ?? 'report-agent';
if (!destination) throw new Error('Provide a new directory for the hosted Report Agent');
if (!/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(app)) throw new Error('Provide a DNS-safe app slug');
await fs.mkdir(destination, { mode: 0o700 });
const starter = 'cmd/gregale/templates/agent-stateless-node';
for (const name of ['agent.js', 'store.js', 'platform.js', 'tools.js', 'schema.js']) {
  const file = path.join(starter, name);
  await fs.mkdir(path.dirname(path.join(destination, file)), { recursive: true });
  await fs.copyFile(path.join(root, file), path.join(destination, file));
}
const example = 'examples/gregale-run';
for (const name of ['package.json', 'agent/agent.mjs', 'agent/bridge.mjs', 'agent/trigger.mjs', 'agent/hosted.mjs',
  'agent/report-intake.ts', 'agent/report-regional.ts', 'agent/report-completion.ts', 'scripts/manifests.mjs',
  'web/index.html', 'web/page.js', 'web/page.css', 'web/report-page.mjs', 'sales-report/rows.json']) {
  const file = path.join(example, name);
  await fs.mkdir(path.dirname(path.join(destination, file)), { recursive: true });
  await fs.copyFile(path.join(root, file), path.join(destination, file));
}
const verifier = 'sdk/node/src/webhook.ts';
await fs.mkdir(path.dirname(path.join(destination, verifier)), { recursive: true });
await fs.copyFile(path.join(root, verifier), path.join(destination, verifier));
const starterManifest = await fs.readFile(path.join(root, starter, 'gregale.yaml'), 'utf8');
const nodeManifest = starterManifest.replace(/^  runtime: node(?:22|24)$/m, '  runtime: node24');
if (!/^  runtime: node24$/m.test(nodeManifest)) throw new Error('The Agent starter needs a supported Node runtime');
const manifest = nodeManifest.replace(/^  - kind: realtime$/m, `$&\n    app: ${app}`);
if (manifest === nodeManifest) throw new Error('The Agent starter is missing its realtime trigger');
await fs.writeFile(path.join(destination, 'gregale.yaml'), manifest);
await fs.copyFile(path.join(root, starter, 'package-lock.json'), path.join(destination, 'package-lock.json'));
const { scripts, ...appPackage } = JSON.parse(await fs.readFile(path.join(root, starter, 'package.json'), 'utf8'));
await fs.writeFile(path.join(destination, 'package.json'), JSON.stringify(appPackage, null, 2) + '\n');
const reportSchema = await fs.readFile(path.join(root, example, 'agent/hosted-schema.sql'), 'utf8');
await fs.writeFile(path.join(destination, 'report-db.sql'), agentSchema.trimStart() + '\n' + reportSchema);
await fs.writeFile(path.join(destination, 'report-intake.ts'), "export * from './examples/gregale-run/agent/report-intake.ts';\n");
await fs.writeFile(path.join(destination, 'report-page.mjs'), "export { createReportPage } from './examples/gregale-run/web/report-page.mjs';\n");
await fs.writeFile(path.join(destination, 'handler.js'), "export { handler } from './examples/gregale-run/agent/hosted.mjs';\n");
