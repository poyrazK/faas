import {createDurableEntityRegistry} from '@gregale/sdk-node/preview/durable-entities/runtime';
import {readFile} from 'node:fs/promises';

const project = JSON.parse(await readFile(new URL('gregale.entities.json', import.meta.url), 'utf8'));
if (project.version !== 1 || !Array.isArray(project.handlers) || project.handlers.length === 0 || project.handlers.length > 64) throw new TypeError('invalid entity project');
const definitions = [];
for (const handler of project.handlers) {
  if (typeof handler.module !== 'string' || handler.module.startsWith('/') || handler.module.includes('..') || handler.module.includes('\\') || !/\.(?:mjs|js)$/u.test(handler.module)) throw new TypeError('invalid handler module');
  const exported = await import(new URL(`./${handler.module}`, import.meta.url).href);
  const definition = exported[handler.export];
  if (!definition || definition.namespace !== handler.namespace || definition.schemaVersion !== handler.schema_version) throw new TypeError('registered handler differs from project declaration');
  definitions.push(definition);
}
export const registry = createDurableEntityRegistry(definitions);
