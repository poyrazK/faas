import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, cp, readFile, writeFile, rm, symlink, access } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { fork, spawnSync } from 'node:child_process'
import { validateBundle, validateBaseURL, readPin, verifyFile, verifyLock, hostArchive, run, download } from '../../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'

const sdkBytes = Buffer.from('fixture SDK archive')
const bundle = {
  format: 1, contract: 1, source: { commit: 'a'.repeat(40), epoch: 1757000000 },
  tools: { go: 'go1.25.13', node: '22.19.0', npm: '10.9.3' },
  cli: { version: 'v0.1.0-test.1', action_sha: 'b'.repeat(40), archives: [{ file: 'gregale_0.1.0-test.1_linux_amd64.tar.gz', bytes: 20, sha256: 'c'.repeat(64), os: 'linux', arch: 'amd64' }] },
  sdk: { file: 'gregale-data-0.1.0.tgz', bytes: sdkBytes.length, sha256: createHash('sha256').update(sdkBytes).digest('hex'),
    name: '@gregale/data', version: '0.1.0', integrity: `sha512-${createHash('sha512').update(sdkBytes).digest('base64')}` },
}

async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), 'gregale-artifact-test-'))
  t.after(() => rm(dir, { recursive: true, force: true }))
  return dir
}

test('builder rejects output inside its checkout, including symlink aliases, before invoking build tools', async t => {
  const dir = await fixture(t)
  const repo = join(dir, 'source')
  const tools = join(repo, 'cmd/gregale/templates/data-api-starter/tools')
  await mkdir(tools, { recursive: true })
  await mkdir(join(repo, 'scripts'))
  await cp(fileURLToPath(new URL('../../scripts/build-data-api-bundle.mjs', import.meta.url)), join(repo, 'scripts/build-data-api-bundle.mjs'))
  await cp(fileURLToPath(new URL('../../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs', import.meta.url)), join(tools, 'artifact-lib.mjs'))
  const alias = join(dir, 'source-alias')
  await symlink(repo, alias)
  for (const output of [repo, join(repo, 'new/nested/bundle'), join(alias, 'new/bundle')]) {
    const result = spawnSync(process.execPath, ['scripts/build-data-api-bundle.mjs', '--version', 'v0.0.0-test.1', '--out-dir', output], { cwd: repo, encoding: 'utf8' })
    assert.equal(result.status, 1)
    assert.match(result.stderr, /Output must be outside the source checkout/)
  }
  await assert.rejects(access(join(repo, 'new')), { code: 'ENOENT' })
})

test('bundle rejects unsafe filenames, unsupported contracts and duplicate targets', () => {
  assert.equal(validateBundle(bundle), bundle)
  for (const mutate of [
    b => { b.sdk.file = '../escape.tgz' }, b => { b.sdk.file = 'https://other.test/sdk.tgz' },
    b => { b.contract = 2 }, b => { b.cli.archives.push(b.cli.archives[0]) },
    b => { b.cli.archives[0].arch = 'x64' }, b => { b.cli.archives[0].bytes = 1024 ** 3 },
    b => { b.sdk.integrity = 'sha256-invalid' }, b => { b.sdk.name = 'other' },
    b => { b.source.commit = 'main' }, b => { b.extra = true },
  ]) { const value = structuredClone(bundle); mutate(value); assert.throws(() => validateBundle(value)) }
  const unsupported = structuredClone(bundle)
  unsupported.cli.archives = []
  assert.throws(() => hostArchive(unsupported), /no CLI archive/)
})

test('artifact origins reject credentials, query strings and redirects are not requested', async t => {
  assert.equal(validateBaseURL('https://artifacts.example.test/approved/'), 'https://artifacts.example.test/approved/')
  assert.equal(validateBaseURL(null), null)
  for (const value of ['http://example.test/', 'https://user:password@example.test/', 'https://example.test/?token=x', 'https://example.test/#x', 'https://example.test/no-slash']) assert.throws(() => validateBaseURL(value))
  const dir = await fixture(t)
  // Exercise the downloader's real subprocess boundary without sending a token
  // to any external server. Capture argv and stdin in a disposable curl stand-in.
  const capture = join(dir, 'capture.json')
  await writeFile(join(dir, 'curl'), `#!/usr/bin/env node\nlet input = ''; for await (const chunk of process.stdin) input += chunk; const fs = await import('node:fs/promises'); await fs.writeFile(process.env.ARTIFACT_CAPTURE, JSON.stringify({args:process.argv.slice(2),input})); process.stderr.write(process.env.ARTIFACT_HTTP_STATUS || '200');\n`, { mode: 0o755 })
  const old = { PATH: process.env.PATH, token: process.env.GREGALE_ARTIFACT_TOKEN, capture: process.env.ARTIFACT_CAPTURE, status: process.env.ARTIFACT_HTTP_STATUS }
  t.after(() => {
    process.env.PATH = old.PATH
    for (const [key, value] of [['GREGALE_ARTIFACT_TOKEN', old.token], ['ARTIFACT_CAPTURE', old.capture], ['ARTIFACT_HTTP_STATUS', old.status]]) {
      if (value === undefined) delete process.env[key]; else process.env[key] = value
    }
  })
  process.env.PATH = `${dir}:${old.PATH}`
  process.env.GREGALE_ARTIFACT_TOKEN = 'test-artifact-bearer'
  process.env.ARTIFACT_CAPTURE = capture
  await download('https://artifacts.example.test/approved/', bundle.sdk, join(dir, 'out'))
  const request = JSON.parse(await readFile(capture, 'utf8'))
  assert.equal(request.args[0], '-q')
  assert.equal(request.args.includes('--location'), false)
  assert.equal(request.args.includes('-L'), false)
  assert.equal(request.args.join(' ').includes(process.env.GREGALE_ARTIFACT_TOKEN), false)
  assert.match(request.input, /Authorization: Bearer test-artifact-bearer/)
  assert.equal(request.args.at(-1), 'https://artifacts.example.test/approved/gregale-data-0.1.0.tgz')
  process.env.ARTIFACT_HTTP_STATUS = '302'
  await assert.rejects(download('https://artifacts.example.test/approved/', bundle.sdk, join(dir, 'out')), /HTTP 200/)
  process.env.GREGALE_ARTIFACT_TOKEN = 'bad\nInjected: header'
  await assert.rejects(download('https://artifacts.example.test/approved/', bundle.sdk, join(dir, 'out')), /token encoding/)
})

test('artifact checksums and the committed npm lockfile must agree', async t => {
  const dir = await fixture(t)
  const archive = join(dir, bundle.sdk.file)
  await writeFile(archive, sdkBytes)
  await verifyFile(archive, bundle.sdk)
  await writeFile(archive, Buffer.alloc(sdkBytes.length))
  await assert.rejects(verifyFile(archive, bundle.sdk), /checksum mismatch/)
  await writeFile(archive, 'truncated')
  await assert.rejects(verifyFile(archive, bundle.sdk), /size mismatch/)
  const entry = { version: bundle.sdk.version, resolved: 'file:vendor/gregale-data.tgz', integrity: bundle.sdk.integrity }
  const pkg = { dependencies: { '@gregale/data': 'file:vendor/gregale-data.tgz' } }
  await writeFile(join(dir, 'package.json'), JSON.stringify(pkg))
  const lock = { packages: { '': pkg, 'node_modules/@gregale/data': entry } }
  await writeFile(join(dir, 'package-lock.json'), JSON.stringify(lock))
  await verifyLock(dir, bundle.sdk)
  entry.integrity = 'sha512-other'
  await writeFile(join(dir, 'package-lock.json'), JSON.stringify(lock))
  await assert.rejects(verifyLock(dir, bundle.sdk), /lockfile differs/)
})

test('restore rejects a mismatched lock and corrupt vendor before npm or network', async t => {
  const dir = await fixture(t)
  await cp(fileURLToPath(new URL('../../cmd/gregale/templates/data-api-starter/tools', import.meta.url)), join(dir, 'tools'), { recursive: true })
  await mkdir(join(dir, 'client/vendor'), { recursive: true })
  const pkg = { dependencies: { '@gregale/data': 'file:vendor/gregale-data.tgz' } }
  await writeFile(join(dir, 'client/package.json'), JSON.stringify(pkg))
  const lock = { packages: { '': pkg, 'node_modules/@gregale/data': { version: '0.1.0', resolved: 'file:vendor/gregale-data.tgz', integrity: 'wrong' } } }
  await writeFile(join(dir, 'client/package-lock.json'), JSON.stringify(lock))
  await writeFile(join(dir, 'data-api-artifacts.json'), JSON.stringify({ format: 1, base_url: null, bundle }))
  await readPin(join(dir, 'data-api-artifacts.json'))
  let result = spawnSync(process.execPath, ['tools/artifacts.mjs', 'restore'], { cwd: dir, encoding: 'utf8' })
  assert.equal(result.status, 1)
  assert.match(result.stderr, /lockfile differs/)
  lock.packages['node_modules/@gregale/data'].integrity = bundle.sdk.integrity
  await writeFile(join(dir, 'client/package-lock.json'), JSON.stringify(lock))
  await writeFile(join(dir, 'client/vendor/gregale-data.tgz'), Buffer.alloc(sdkBytes.length))
  result = spawnSync(process.execPath, ['tools/artifacts.mjs', 'restore'], { cwd: dir, encoding: 'utf8' })
  assert.equal(result.status, 1)
  assert.match(result.stderr, /checksum mismatch/)
  await writeFile(join(dir, 'client/vendor/gregale-data.tgz'), sdkBytes)
  assert.match(run(process.execPath, ['tools/artifacts.mjs', 'restore'], { cwd: dir }), /Verified and restored/)
})

test('real HTTPS restoration verifies bytes and refuses cross-origin redirects', { timeout: 30000 }, async t => {
  const dir = await fixture(t)
  run('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', join(dir, 'key.pem'), '-out', join(dir, 'cert.pem'),
    '-days', '1', '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1'])
  await writeFile(join(dir, 'server.mjs'), `
import https from 'node:https'; import fs from 'node:fs';
const requests = []; const body = Buffer.from('fixture SDK archive');
const options = {key:fs.readFileSync('key.pem'),cert:fs.readFileSync('cert.pem')};
let otherPort;
const other = https.createServer(options, (req,res) => {requests.push({other:true,auth:req.headers.authorization}); fs.writeFileSync('requests.json',JSON.stringify(requests)); res.end(body)});
await new Promise(resolve => other.listen(0,'127.0.0.1',resolve)); otherPort = other.address().port;
const server = https.createServer(options, (req,res) => {
 requests.push({path:req.url,auth:req.headers.authorization,cookie:req.headers.cookie}); fs.writeFileSync('requests.json',JSON.stringify(requests));
 if (req.url.startsWith('/redirect/')) {res.writeHead(302,{Location:'https://127.0.0.1:'+otherPort+'/sdk'});res.end()}
 else if(req.url.startsWith('/corrupt/')) res.end(Buffer.alloc(body.length));
 else if(req.url.startsWith('/oversize/')) {res.writeHead(200,{'Transfer-Encoding':'chunked'});res.end(Buffer.alloc(body.length+1))}
 else if(req.url.startsWith('/missing/')) {res.writeHead(404);res.end('private failure body')}
 else res.end(body);
});
await new Promise(resolve => server.listen(0,'127.0.0.1',resolve)); process.send({port:server.address().port});
`)
  const server = fork(join(dir, 'server.mjs'), { cwd: dir, stdio: ['ignore', 'ignore', 'pipe', 'ipc'] })
  t.after(() => server.kill())
  const { port } = await new Promise((resolve, reject) => { server.once('message', resolve); server.once('error', reject); server.once('exit', code => reject(new Error(`HTTPS fixture exited ${code}`))) })
  const env = { ...process.env, CURL_CA_BUNDLE: join(dir, 'cert.pem'), NO_PROXY: '127.0.0.1', no_proxy: '127.0.0.1', GREGALE_ARTIFACT_TOKEN: 'local-fixture-artifact-token' }
  await cp(fileURLToPath(new URL('../../cmd/gregale/templates/data-api-starter/tools', import.meta.url)), join(dir, 'tools'), { recursive: true })
  await mkdir(join(dir, 'client'))
  const pkg = { dependencies: { '@gregale/data': 'file:vendor/gregale-data.tgz' } }
  await writeFile(join(dir, 'client/package.json'), JSON.stringify(pkg))
  await writeFile(join(dir, 'client/package-lock.json'), JSON.stringify({ packages: { '': pkg, 'node_modules/@gregale/data': { version: '0.1.0', resolved: 'file:vendor/gregale-data.tgz', integrity: bundle.sdk.integrity } } }))
  for (const [route, expected] of [['redirect/', /HTTP 200/], ['missing/', /curl command failed/], ['corrupt/', /checksum mismatch/], ['oversize/', /size mismatch|curl command failed/], ['', /Verified and restored/]]) {
    await writeFile(join(dir, 'data-api-artifacts.json'), JSON.stringify({ format: 1, base_url: `https://127.0.0.1:${port}/${route}`, bundle }))
    const result = spawnSync(process.execPath, ['tools/artifacts.mjs', 'restore'], { cwd: dir, env, encoding: 'utf8', timeout: 10000 })
    assert.equal(result.status, route ? 1 : 0, result.stderr)
    assert.match(route ? result.stderr : result.stdout, expected)
    assert.equal((result.stderr + result.stdout).includes(env.GREGALE_ARTIFACT_TOKEN), false)
    assert.equal(result.stderr.includes('private failure body'), false)
  }
  assert.deepEqual(await readFile(join(dir, 'client/vendor/gregale-data.tgz')), sdkBytes)
  const requests = JSON.parse(await readFile(join(dir, 'requests.json'), 'utf8'))
  assert.equal(requests.length, 5)
  assert.equal(requests.some(request => request.other), false)
  for (const request of requests) {
    assert.equal(request.auth, 'Bearer local-fixture-artifact-token')
    assert.equal(request.cookie, undefined)
  }
})
