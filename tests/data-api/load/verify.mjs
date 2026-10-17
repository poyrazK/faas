#!/usr/bin/env node
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { options, failureCode, check, writeEvidence } from './contracts.mjs'
import { LocalFixture, localSettings } from './local.mjs'
import { verifyLoad } from './runner.mjs'

export function argumentsFor(argv) {
  const raw = {}; let output
  for (let i = 0; i < argv.length; i += 2) {
    const flag = argv[i], value = argv[i + 1]
    check(['--output', '--requests', '--concurrency', '--users'].includes(flag) && typeof value === 'string' && value.length > 0, 'invalid_options')
    if (flag === '--output') { check(!output && value !== '-', 'invalid_options'); output = resolve(value) }
    else { const key = flag.slice(2); check(!Object.hasOwn(raw, key) && /^[1-9]\d*$/.test(value), 'invalid_options'); raw[key] = Number(value) }
  }
  check(output, 'invalid_options')
  return { output, workload: options(raw) }
}
export async function main(argv = process.argv.slice(2), env = process.env) {
  const controller = new AbortController(), interrupt = () => controller.abort()
  process.once('SIGINT', interrupt); process.once('SIGTERM', interrupt)
  try {
    const { output, workload } = argumentsFor(argv), settings = localSettings(env)
    const fixture = new LocalFixture(settings, workload)
    let progress = ''
    const report = await verifyLoad(fixture, workload, { signal: controller.signal, save: async value => {
      await writeEvidence(output, value)
      const phase = value.scenarios.at(-1), current = phase ? `${phase.name}:${phase.result}` : 'setup:running'
      if (current !== progress) { process.stderr.write(`data_api_load ${current}\n`); progress = current }
    } })
    process.stdout.write(JSON.stringify({ result: report.result, failure: report.failure?.code ?? null, cleanup: report.cleanup.state }) + '\n')
    return report.result === 'passed' ? 0 : 1
  } catch (error) { process.stderr.write(`data_api_load ${failureCode(error)}\n`); return 1 }
  finally { process.removeListener('SIGINT', interrupt); process.removeListener('SIGTERM', interrupt) }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) process.exitCode = await main()
