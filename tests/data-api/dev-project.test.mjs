import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, writeFile, rm, link } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const runtime = process.env.DATA_API_RUNTIME_DIR
const projectModule = runtime ? await import(pathToFileURL(join(runtime, 'dev-project.mjs'))) : null
async function fixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-dev-project-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  await mkdir(join(directory, 'sql'))
  const config = { version: 1, migrations: 'sql', output: 'database.types.ts', identities: { buyer: { subject: 'customer-a', claims: { tenant: 'shop-a' } }, other: { subject: 'customer-b' } }, verify: { collection: 'checks.json', scenarios: ['isolation'] } }
  const requests = ['buyer', 'other'].map(identity => ({ name: identity, identity, method: 'GET', path: '/rest/v1/orders', expect: { status: 200, json: [] } }))
  const save = () => writeFile(join(directory, 'data-api.dev.json'), JSON.stringify(config))
  await save()
  await writeFile(join(directory, 'checks.json'), JSON.stringify({ version: 2, scenarios: [{ name: 'isolation', requests }] }))
  return { directory, output: join(directory, 'default.types.ts'), definition: config, save }
}

test('arbitrary projects resolve migrations, identities, claims, seed and output', { skip: !runtime }, async t => {
  const f = await fixture(t)
  f.definition.seed = 'seed.sql'; await writeFile(join(f.directory, 'seed.sql'), 'SELECT 1;'); await f.save()
  const project = await projectModule.loadDevProject(f)
  assert.equal(project.migrations, join(f.directory, 'sql'))
  assert.equal(project.output, join(f.directory, 'database.types.ts'))
  assert.deepEqual(project.identities.buyer, { subject: 'customer-a', claims: { tenant: 'shop-a' } })
  assert.equal(project.seedSQL, 'SELECT 1;')
  assert.equal((await projectModule.loadDevProject({ ...f, output_set: true })).output, f.output)
})

test('required checks cannot be empty, missing or restricted to one subject', { skip: !runtime }, async t => {
  const f = await fixture(t)
  for (const scenarios of [[], ['missing']]) {
    f.definition.verify.scenarios = scenarios; await f.save()
    await assert.rejects(projectModule.loadDevProject(f))
  }
  f.definition.verify.scenarios = ['isolation']; await f.save()
  await writeFile(join(f.directory, 'checks.json'), JSON.stringify({ version: 2, scenarios: [{ name: 'isolation', requests: [{ name: 'only buyer', identity: 'buyer', method: 'GET', path: '/rest/v1/orders', expect: { status: 200 } }] }] }))
  await assert.rejects(projectModule.loadDevProject(f), /two distinct subjects/)
})

test('reserved claims, unknown identities and paths outside the project fail closed', { skip: !runtime }, async t => {
  const f = await fixture(t)
  f.definition.identities.buyer.claims = { exp: 1 }; await f.save()
  await assert.rejects(projectModule.loadDevProject(f), /non-reserved/)
  f.definition.identities.buyer.claims = {}; f.definition.migrations = '..'; await f.save()
  await assert.rejects(projectModule.loadDevProject(f), /inside the project/)
  f.definition.migrations = 'sql'; await f.save()
  await writeFile(join(f.directory, 'checks.json'), JSON.stringify({ version: 1, requests: [{ name: 'unknown', identity: 'alice', method: 'GET', path: '/rest/v1/orders', expect: { status: 200 } }] }))
  await assert.rejects(projectModule.loadDevProject(f), /Cannot read/)
})

test('types cannot overwrite project sources or verification through hardlinks', { skip: !runtime }, async t => {
  const f = await fixture(t)
  f.definition.output = 'checks.json'; await f.save()
  await assert.rejects(projectModule.loadDevProject(f), /must not replace/)
  f.definition.output = 'alias.json'; await link(join(f.directory, 'checks.json'), join(f.directory, 'alias.json')); await f.save()
  await assert.rejects(projectModule.loadDevProject(f), /must not replace/)
})

test('read-only identities preserve defaults and cannot forge the reserved read-only JWT claim', {skip:!runtime},async t=>{
 const f=await fixture(t);f.definition.identities.buyer.read_only=true;await f.save()
 const project=await projectModule.loadDevProject(f);assert.equal(project.identities.buyer.read_only,true);assert.equal(project.identities.other.read_only,undefined)
 for(const value of ['true',1,null]){f.definition.identities.buyer.read_only=value;await f.save();await assert.rejects(projectModule.loadDevProject(f))}
 f.definition.identities.buyer.read_only=true;f.definition.identities.buyer.claims={gregale_read_only:false};await f.save();await assert.rejects(projectModule.loadDevProject(f))
})

test('anonymous settings use the hosted allowlist formats', { skip: !runtime }, async t => {
  const f = await fixture(t)
  f.definition.public_read = ['api.products']; f.definition.public_rpc = ['api.search_products', 'api.featured']; await f.save()
  const project = await projectModule.loadDevProject(f)
  assert.equal(project.publicRead, 'api.products')
  assert.equal(project.publicRpc, 'api.search_products,api.featured')
  for (const value of [[], 'api.search_products', [1]]) {
    f.definition.public_rpc = value; await f.save()
    await assert.rejects(projectModule.loadDevProject(f), /public_rpc/)
  }
})
