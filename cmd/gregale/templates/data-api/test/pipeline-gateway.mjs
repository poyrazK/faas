// Disposable native pipeline acceptance fixture; never part of runtime startup.
import { spawn } from 'node:child_process'
import net from 'node:net'
import { randomBytes } from 'node:crypto'
import { SignJWT, jwtVerify } from 'jose'
import { runtimeConfig } from '../config.mjs'
import { inspect, fingerprint } from '../types.mjs'
import { createServer } from '../server.mjs'

const reserve = async () => {
  const listener = net.createServer()
  await new Promise(resolve => listener.listen(0, '127.0.0.1', resolve))
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve))
  return port
}
const config = runtimeConfig(process.env)
config.snapshot = await inspect(config.connection, config.schemas)
config.functions = config.snapshot.functions
config.fingerprint = fingerprint(config.snapshot)
const port = await reserve(), adminPort = await reserve()
const engine = spawn(process.env.POSTGREST_BINARY, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(port), PGRST_ADMIN_SERVER_PORT: String(adminPort) }, stdio: ['ignore', 'ignore', 'pipe'] })
engine.stderr.resume()
const stop = () => { engine.kill('SIGTERM'); gateway?.closeAllConnections(); gateway?.close(); setTimeout(() => process.exit(0), 200).unref() }
let gateway
process.on('SIGTERM', stop)
process.on('SIGINT', stop)
try {
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    if (engine.exitCode !== null) throw new Error('PostgREST exited before readiness')
    try { ready = (await fetch(`http://127.0.0.1:${adminPort}/ready`)).ok } catch {}
    if (ready) break
    await new Promise(resolve => setTimeout(resolve, 50))
  }
  if (!ready) throw new Error('PostgREST did not become ready')
  const key = randomBytes(32)
  const tokens = {}
  // Each Go native journey has a five-minute deadline. Keep its fixture
  // identities valid through late quarantine and recovery assertions.
  for (const sub of ['alice', 'bob']) tokens[sub] = await new SignJWT({ sub }).setProtectedHeader({ alg: 'HS256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(key)
  gateway = createServer(config, async token => (await jwtVerify(token, key, { issuer: config.auth.issuer, audience: config.auth.audience, algorithms: ['HS256'] })).payload, port, adminPort)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  process.stdout.write(`${JSON.stringify({ port: gateway.address().port, fingerprint: config.fingerprint, ...tokens })}\n`)
} catch {
  engine.kill('SIGTERM')
  // Do not echo engine diagnostics: database errors may contain credentials.
  process.stderr.write('Native pipeline HTTP fixture failed to start\n')
  process.exitCode = 1
}
