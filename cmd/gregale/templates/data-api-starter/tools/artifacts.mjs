import { copyFile, mkdir, mkdtemp, rename, rm, lstat, writeFile, realpath } from 'node:fs/promises'
import { dirname, join, resolve, relative, isAbsolute, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { tmpdir } from 'node:os'
import { validateBundle, validateBaseURL, readJSON, readPin, verifyFile, verifyLock, hostArchive, run, download } from './artifact-lib.mjs'

async function exists(file) {
  try {
    if (!(await lstat(file)).isFile()) throw new Error('Artifact destination must be a regular file')
    return true
  } catch (error) { if (error.code === 'ENOENT') return false; throw error }
}

async function atomicCopy(from, to) {
  await mkdir(dirname(to), { recursive: true })
  const temp = `${to}.new-${process.pid}`
  try { await copyFile(from, temp); await rename(temp, to) } finally { await rm(temp, { force: true }) }
}

async function obtain(entry, work, from, base) {
  const file = join(work, entry.file)
  if (from) await copyFile(join(from, entry.file), file)
  else {
    if (!base) throw new Error('No artifact source; supply --from or configure base_url when pinning')
    await download(base, entry, file)
  }
  await verifyFile(file, entry)
  return file
}

async function installCLI(file, bundle, work, root) {
  // Never extract arbitrary archive paths or links. Only one ordinary binary
  // is allowed, then its embedded source/version must match the pinned pair.
  const list = run('tar', ['-tzf', file]).trim()
  const detail = run('tar', ['-tvzf', file]).trim()
  if (list !== 'gregale' || !detail.startsWith('-')) throw new Error('CLI archive must contain one regular gregale binary')
  const dir = join(work, 'cli')
  await mkdir(dir)
  run('tar', ['-xzf', file, '-C', dir, '--no-same-owner'])
  const binary = join(dir, 'gregale')
  const version = JSON.parse(run(binary, ['version', '--json'], { env: { ...process.env, FAAS_JSON: '1' } }))
  if (version.version !== bundle.cli.version || version.git_sha !== bundle.source.commit ||
      version.build_time !== new Date(bundle.source.epoch * 1000).toISOString().replace('.000Z', 'Z')) throw new Error('CLI embedded version differs from pinned bundle')
  await atomicCopy(binary, join(root, '.gregale-tools/gregale'))
}

export async function main(args, root = fileURLToPath(new URL('../', import.meta.url)), clientDirectory = 'client') {
  root = await realpath(root)
  const client = await realpath(resolve(root, clientDirectory))
  const rel = relative(root, client)
  if (isAbsolute(clientDirectory) || rel === '..' || rel.startsWith('..' + sep)) throw new Error('Client directory must remain inside the project')
  let vendor
  try { vendor = await realpath(join(client, 'vendor')) }
  catch (error) { if (error.code !== 'ENOENT') throw error; vendor = join(client, 'vendor') }
  const vendorRelative = relative(client, vendor)
  if (isAbsolute(vendorRelative) || vendorRelative === '..' || vendorRelative.startsWith('..' + sep)) throw new Error('Vendor directory must remain inside the client')
  const sdkTarget = join(vendor, 'gregale-data.tgz')
  const pinTarget = join(root, 'data-api-artifacts.json')
  const mode = args.shift()
  let source, base = null, withCLI = false, sdkOnly = false
  if (mode === 'pin') source = args.shift()
  if (!['pin', 'restore'].includes(mode) || (mode === 'pin' && !source)) throw new Error('Usage: node tools/artifacts.mjs pin MANIFEST [--base-url HTTPS_URL] | restore [--from BUNDLE_DIR] [--cli]')
  while (args.length) {
    const flag = args.shift()
    if (flag === '--cli' && mode === 'restore') withCLI = true
    else if (flag === '--sdk-only') sdkOnly = true
    else if (flag === '--from' && mode === 'restore' && args.length) source = args.shift()
    else if (flag === '--base-url' && mode === 'pin' && args.length) base = validateBaseURL(args.shift())
    else throw new Error('Unknown or incomplete artifact option')
  }
  const pin = mode === 'pin'
    ? { format: 1, base_url: base, bundle: validateBundle(await readJSON(resolve(source))) }
    : await readPin(pinTarget)
  const from = source ? mode === 'pin' ? dirname(resolve(source)) : resolve(source) : null
  const work = await mkdtemp(join(tmpdir(), 'gregale-artifacts-'))
  try {
    if (mode === 'restore') await verifyLock(client, pin.bundle.sdk)
    const archive = !sdkOnly && (mode === 'pin' || withCLI) ? hostArchive(pin.bundle) : null
    // Fetch and verify every requested artifact before changing the client.
    const sdk = mode === 'restore' && await exists(sdkTarget)
      ? (await verifyFile(sdkTarget, pin.bundle.sdk), sdkTarget)
      : await obtain(pin.bundle.sdk, work, from, pin.base_url)
    const cli = archive ? await obtain(archive, work, from, pin.base_url) : null
    if (cli) await installCLI(cli, pin.bundle, work, root)
    if (sdk !== sdkTarget) await atomicCopy(sdk, sdkTarget)
    if (mode === 'pin') {
      run('npm', ['install', '--save-exact', '--ignore-scripts', '--no-audit', '--no-fund', './vendor/gregale-data.tgz'], { cwd: client, stdio: 'inherit' })
      await verifyLock(client, pin.bundle.sdk)
      const temp = `${pinTarget}.new-${process.pid}`
      try { await writeFile(temp, `${JSON.stringify(pin, null, 2)}\n`); await rename(temp, pinTarget) } finally { await rm(temp, { force: true }) }
    }
    if (mode === 'restore' && sdkOnly) run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: client, stdio: 'inherit' })
    console.log(mode === 'pin' ? `Pinned and installed Data API ${sdkOnly ? 'SDK' : 'CLI/SDK'} artifacts; commit data-api-artifacts.json and the client package.json and package-lock.json.` : 'Verified and restored pinned Data API artifacts.')
  } finally { await rm(work, { recursive: true, force: true }) }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { await main(process.argv.slice(2)) } catch (error) {
    console.error(`Data API artifacts: ${error.message}`)
    process.exitCode = 1
  }
}
