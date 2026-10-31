import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import tls from 'node:tls'
import { execFileSync, spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

// A hosted Data API reaches its database by name over verified TLS. Every
// other test uses a localhost database, which the runtime leaves alone, so this
// one puts a TLS-terminating PostgreSQL front end with a certificate for
// db.data-api.test in front of the test server and lets libpq reach it through
// hostaddr.

const host = 'db.data-api.test'
const sslRequest = 80877103, gssRequest = 80877104

// certificates makes a one-day CA and a server certificate for host.
function certificates(dir) {
  const run = args => execFileSync('openssl', args, { cwd: dir, stdio: ['ignore', 'ignore', 'pipe'] })
  writeFileSync(join(dir, 'server.ext'), `subjectAltName=DNS:${host}\nextendedKeyUsage=serverAuth\n`)
  run(['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=Data API test CA', '-keyout', 'ca.key', '-out', 'ca.crt',
    '-addext', 'basicConstraints=critical,CA:true', '-addext', 'keyUsage=critical,keyCertSign'])
  run(['req', '-newkey', 'rsa:2048', '-nodes', '-subj', `/CN=${host}`, '-keyout', 'server.key', '-out', 'server.csr'])
  run(['x509', '-req', '-in', 'server.csr', '-CA', 'ca.crt', '-CAkey', 'ca.key', '-CAcreateserial', '-days', '1', '-extfile', 'server.ext', '-out', 'server.crt'])
  return { ca: join(dir, 'ca.crt'), key: readFileSync(join(dir, 'server.key')), cert: readFileSync(join(dir, 'server.crt')) }
}

// frontEnd answers PostgreSQL's SSLRequest, terminates TLS with the test
// certificate and relays the decrypted protocol to the plaintext test server.
async function frontEnd(target, key, cert) {
  const sessions = { tls: 0 }
  const server = net.createServer(socket => {
    let buffered = Buffer.alloc(0)
    const onData = chunk => {
      buffered = Buffer.concat([buffered, chunk])
      while (buffered.length >= 8) {
        const code = buffered.readUInt32BE(4)
        if (buffered.readUInt32BE(0) !== 8 || (code !== sslRequest && code !== gssRequest)) return socket.destroy()
        buffered = buffered.subarray(8)
        if (code === gssRequest) { socket.write('N'); continue }
        socket.off('data', onData)
        socket.write('S')
        const secure = new tls.TLSSocket(socket, { isServer: true, key, cert })
        secure.once('secure', () => { sessions.tls++ })
        const upstream = net.connect(target.port, target.hostname)
        secure.pipe(upstream).pipe(secure)
        const close = () => { secure.destroy(); upstream.destroy() }
        secure.on('error', close); upstream.on('error', close); secure.on('close', close); upstream.on('close', close)
        return
      }
    }
    socket.on('data', onData)
    socket.on('error', () => socket.destroy())
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  return { server, sessions, port: server.address().port }
}

async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  await new Promise(resolve => server.close(resolve))
  return port
}

async function engineReady(env, waitMs) {
  const ready = await freePort()
  const child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...env, PGRST_SERVER_PORT: String(await freePort()), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''
  child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  let status = 0
  for (const deadline = Date.now() + waitMs; Date.now() < deadline && child.exitCode === null; await new Promise(r => setTimeout(r, 100))) {
    status = await fetch(`http://127.0.0.1:${ready}/ready`).then(r => r.status, () => 0)
    if (status === 200) break
  }
  child.kill('SIGTERM')
  await once(child, 'exit')
  return { status, logs }
}

test('PostgREST verifies a remote database by name with the roots the runtime trusts', { skip: !enabled, timeout: 90000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime))
  const { default: pg } = await import(pathToFileURL(require.resolve('pg')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { ca, key, cert } = certificates(mkdtempSync(join(tmpdir(), 'data-api-remote-tls-')))

  const suffix = `${process.pid}_${Date.now()}`, database = `remote_tls_${suffix}`, role = `remote_tls_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  const target = new URL(process.env.DATA_API_TEST_DATABASE_URL)
  const front = await frontEnd(target, key, cert)
  t.after(async () => {
    front.server.close()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  const owner = new pg.Client({ connectionString: ownerURL.toString() })
  await owner.connect()
  await owner.query(`CREATE SCHEMA api; CREATE TABLE api.ping (id int); GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT ON api.ping TO "${role}"`)
  await owner.end()

  // What the binding delivers: the provider's name and sslmode=require.
  const delivered = `postgresql://${role}:test-only@${host}:${front.port}/${database}?hostaddr=127.0.0.1&sslmode=require`
  const auth = { DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }
  const home = mkdtempSync(join(tmpdir(), 'data-api-remote-tls-home-'))
  const config = runtimeConfig({ ...auth, DATABASE_URL: delivered, NODE_EXTRA_CA_CERTS: ca })
  assert.equal(new URL(config.connection).searchParams.get('sslmode'), 'verify-full')

  // No ~/.postgresql/root.crt: the engine trusts only what the runtime gives it.
  const trusted = await engineReady({ ...process.env, HOME: home, ...config.postgrestEnv }, 30000)
  assert.equal(trusted.status, 200, trusted.logs)
  assert.ok(front.sessions.tls > 0, 'the engine spoke TLS to the front end')

  // A store without the database's CA must refuse it: verify-full is real.
  const untrusted = runtimeConfig({ ...auth, DATABASE_URL: delivered })
  const refused = await engineReady({ ...process.env, HOME: home, ...untrusted.postgrestEnv }, 4000)
  assert.notEqual(refused.status, 200)
})
