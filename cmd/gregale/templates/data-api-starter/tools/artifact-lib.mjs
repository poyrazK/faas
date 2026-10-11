import { createHash } from 'node:crypto'
import { readFile, stat, writeFile } from 'node:fs/promises'
import { spawn, spawnSync } from 'node:child_process'

const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$/
const targets = new Set(['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'])

function object(value, keys) {
  if (!value || typeof value !== 'object' || Array.isArray(value) ||
      Object.keys(value).sort().join() !== [...keys].sort().join()) throw new Error('Invalid artifact manifest fields')
}

function artifact(value, extension, limit, extra = []) {
  object(value, ['file', 'bytes', 'sha256', ...extra])
  if (typeof value.file !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(value.file) || !value.file.endsWith(extension) ||
      !Number.isSafeInteger(value.bytes) || value.bytes <= 0 || value.bytes > limit || !/^[a-f0-9]{64}$/.test(value.sha256)) {
    throw new Error('Invalid artifact filename, size or checksum')
  }
}

export function validateBundle(bundle) {
  object(bundle, ['format', 'contract', 'source', 'tools', 'cli', 'sdk'])
  if (bundle.format !== 1 || bundle.contract !== 1) throw new Error('Unsupported Data API bundle format or contract')
  object(bundle.source, ['commit', 'epoch'])
  if (!/^[a-f0-9]{40}$/.test(bundle.source.commit) || !Number.isSafeInteger(bundle.source.epoch) || bundle.source.epoch < 0) throw new Error('Invalid bundle source')
  object(bundle.tools, ['go', 'node', 'npm'])
  if (!/^go1\.\d+\.\d+$/.test(bundle.tools.go) || !/^\d+\.\d+\.\d+$/.test(bundle.tools.node) || !/^\d+\.\d+\.\d+$/.test(bundle.tools.npm)) throw new Error('Invalid build tool versions')
  object(bundle.cli, ['version', 'action_sha', 'archives'])
  if (!semver.test(bundle.cli.version?.replace(/^v/, '')) || !/^v/.test(bundle.cli.version) || !/^[a-f0-9]{40}$/.test(bundle.cli.action_sha) ||
      !Array.isArray(bundle.cli.archives) || bundle.cli.archives.length < 1 || bundle.cli.archives.length > 4) throw new Error('Invalid CLI metadata')
  const seen = new Set()
  for (const archive of bundle.cli.archives) {
    artifact(archive, '.tar.gz', 128 * 1024 * 1024, ['os', 'arch'])
    const target = `${archive.os}/${archive.arch}`
    if (!targets.has(target) || seen.has(target) || archive.file !== `gregale_${bundle.cli.version.slice(1)}_${archive.os}_${archive.arch}.tar.gz`) throw new Error('Invalid or duplicate CLI target')
    seen.add(target)
  }
  artifact(bundle.sdk, '.tgz', 8 * 1024 * 1024, ['name', 'version', 'integrity'])
  if (bundle.sdk.name !== '@gregale/data' || !semver.test(bundle.sdk.version) ||
      bundle.sdk.file !== `gregale-data-${bundle.sdk.version}.tgz` || !/^sha512-[A-Za-z0-9+/]{86}==$/.test(bundle.sdk.integrity)) throw new Error('Invalid SDK metadata')
  return bundle
}

export function validateBaseURL(value) {
  if (value === null) return value
  let url
  try { url = new URL(value) } catch { throw new Error('Artifact base URL must be HTTPS') }
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || !url.pathname.endsWith('/')) throw new Error('Artifact base URL must be HTTPS with a trailing slash and no credentials, query or fragment')
  return url.href
}

export async function readJSON(file, limit = 64 * 1024) {
  if ((await stat(file)).size > limit) throw new Error('Artifact metadata exceeds its size limit')
  return JSON.parse(await readFile(file, 'utf8'))
}

export async function readPin(file) {
  const pin = await readJSON(file)
  object(pin, ['format', 'base_url', 'bundle'])
  if (pin.format !== 1) throw new Error('Unsupported artifact pin format')
  validateBaseURL(pin.base_url)
  validateBundle(pin.bundle)
  return pin
}

export async function verifyFile(file, entry) {
  const info = await stat(file)
  if (!info.isFile() || info.size !== entry.bytes) throw new Error(`Artifact size mismatch: ${entry.file}`)
  const data = await readFile(file)
  if (createHash('sha256').update(data).digest('hex') !== entry.sha256 ||
      (entry.integrity && `sha512-${createHash('sha512').update(data).digest('base64')}` !== entry.integrity)) throw new Error(`Artifact checksum mismatch: ${entry.file}`)
}

export function hostArchive(bundle) {
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch]
  const entry = bundle.cli.archives.find(entry => entry.os === process.platform && entry.arch === arch)
  if (!entry) throw new Error('Bundle has no CLI archive for this platform')
  return entry
}

export async function verifyLock(client, sdk) {
  const lock = await readJSON(`${client}/package-lock.json`, 16 * 1024 * 1024)
  const pkg = await readJSON(`${client}/package.json`)
  const entry = lock.packages?.['node_modules/@gregale/data']
  if (pkg.dependencies?.['@gregale/data'] !== 'file:vendor/gregale-data.tgz' ||
      lock.packages?.['']?.dependencies?.['@gregale/data'] !== 'file:vendor/gregale-data.tgz' ||
      entry?.version !== sdk.version || entry?.resolved !== 'file:vendor/gregale-data.tgz' || entry?.integrity !== sdk.integrity) {
    throw new Error('Client lockfile differs from the pinned SDK; run artifacts.mjs pin and commit the updated lockfile')
  }
}

export function run(binary, args, options = {}) {
  const result = spawnSync(binary, args, { encoding: 'utf8', maxBuffer: 1024 * 1024, timeout: 120000, ...options })
  if (result.error || result.status !== 0) throw new Error(`${binary} command failed`)
  return result.stdout
}

// curl uses the environment's proxy and CA trust. The optional bearer stays in
// stdin, never in argv or diagnostics. No redirect, cookie jar or curlrc is used.
export async function download(base, entry, output) {
  const url = new URL(entry.file, validateBaseURL(base)).href
  const token = process.env.GREGALE_ARTIFACT_TOKEN
  if (token && !/^[A-Za-z0-9._~+/-]+=*$/.test(token)) throw new Error('Invalid artifact token encoding')
  const config = token ? `header = "Authorization: Bearer ${token}"\n` : ''
  const data = await new Promise((resolve, reject) => {
    const child = spawn('curl', ['-q', '--config', '-', '--proto', '=https', '--fail', '--silent', '--show-error',
      '--max-time', '60', '--max-filesize', String(entry.bytes), '--output', '-', '--write-out', '%{stderr}%{http_code}', url])
    let bytes = 0, diagnostics = '', failure
    const chunks = []
    const timer = setTimeout(() => { failure = new Error('Artifact download timed out'); child.kill('SIGKILL') }, 65000)
    child.stdout.on('data', chunk => {
      bytes += chunk.length
      // Also bound chunked responses on older curl versions that only enforce
      // --max-filesize when the server supplies Content-Length.
      if (bytes > entry.bytes) { failure = new Error(`Artifact size mismatch: ${entry.file}`); child.kill('SIGKILL') }
      else chunks.push(chunk)
    })
    child.stderr.on('data', chunk => {
      if (diagnostics.length + chunk.length > 8192) { failure = new Error('Artifact download failed'); child.kill('SIGKILL') }
      else diagnostics += chunk.toString()
    })
    child.once('error', () => { failure = new Error('curl command failed') })
    child.once('close', code => {
      clearTimeout(timer)
      if (failure) reject(failure)
      else if (code !== 0) reject(new Error('curl command failed'))
      else if (diagnostics !== '200') reject(new Error('Artifact download did not return HTTP 200'))
      else resolve(Buffer.concat(chunks))
    })
    child.stdin.on('error', () => {})
    child.stdin.end(config)
  })
  await writeFile(output, data)
}
