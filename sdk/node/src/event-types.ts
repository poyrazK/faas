#!/usr/bin/env node
/** Reuse the portable Job schema renderer for event payloads. */
import { readFile, writeFile, realpath } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import { EventContract, type EventContractSpec } from './event-contract.js';
import { generateJobTypes } from './job-types.js';

export function generateEventTypes(spec: EventContractSpec, language: 'typescript' | 'python'): string {
  spec = new EventContract(spec).spec;
  const generated = generateJobTypes({ version: 1, name: 'event-payload', input: spec.data, output: { type: 'null' } }, language);
  // Only rewrite renderer-owned lines; never replace inside schema/property strings.
  const lines = generated.trimEnd().split('\n');
  lines.pop();
  if (language === 'typescript') {
    return lines.filter(line => line !== 'export type Output = null;').map(line =>
      line === '// Generated from a Gregale job contract.' ? '// Generated from a Gregale event contract.' :
      line === 'import {JobContract} from "@gregale/sdk-node/jobs/runtime";' ? 'import {EventContract} from "@gregale/sdk-node/preview/events/runtime";' : line
    ).join('\n') + '\nexport const contract = new EventContract<Input>(' + JSON.stringify(spec) + ' as const);\n';
  }
  return lines.filter(line => line !== 'Output: TypeAlias = None').map(line =>
    line === '# Generated from a Gregale job contract.' ? '# Generated from a Gregale event contract.' :
    line === 'from faas_sdk.job_runtime import JobContract' ? 'from faas_sdk.preview.event_contract import EventContract' : line
  ).join('\n') + '\ncontract: EventContract[Input] = EventContract(json.loads(' + JSON.stringify(JSON.stringify(spec)) + '))\n';
}
if (process.argv[1] && pathToFileURL(await realpath(process.argv[1]).catch(() => '')).href === import.meta.url) {
  try {
    const [source, language, destination] = process.argv.slice(2);
    if (!source || !destination || !['typescript', 'python'].includes(language!)) throw new Error();
    await writeFile(destination, generateEventTypes(JSON.parse(await readFile(source, 'utf8')), language as 'typescript' | 'python'));
  } catch {
    console.error('Use gregale-event-types CONTRACT.json typescript|python OUTPUT; unsupported schemas are refused.');
    process.exitCode = 1;
  }
}
