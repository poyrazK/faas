#!/usr/bin/env node
// Local-only packaging. No registry publication or release upload.
import { createHash } from 'node:crypto'
import { access, mkdir, mkdtemp, readFile, realpath, rename, rm, writeFile } from 'node:fs/promises'
import { basename, dirname, join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { fileURLToPath } from 'node:url'
import { validateBundle, run } from '../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'
import { pinnedGoToolchain } from './data-api-toolchain.mjs'

const repo = await realpath(fileURLToPath(new URL('../', import.meta.url)))

async function canonicalOutput(path) {
  const suffix = []
  for (;;) {
    try { return join(await realpath(path), ...suffix) } catch (error) {
      if (error.code !== 'ENOENT') throw error
      suffix.unshift(basename(path))
      path = dirname(path)
    }
  }
}

async function main(args) {
  const options = {}
  while (args.length) {
    const key = args.shift()
    if (!['--version', '--out-dir', '--targets', '--action-sha'].includes(key) || !args.length || options[key]) throw new Error('Usage: node scripts/build-data-api-bundle.mjs --version vX.Y.Z --out-dir NEW_DIR [--targets linux/amd64,linux/arm64,darwin/amd64,darwin/arm64] [--action-sha FULL_SHA]')
    options[key] = args.shift()
  }
  const version = options['--version']
  if (!/^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$/.test(version) || !options['--out-dir']) throw new Error('A semver CLI version and new output directory are required')
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch]
  const targets = (options['--targets'] || `${process.platform}/${arch}`).split(',').sort()
  if (new Set(targets).size !== targets.length || targets.some(target => !['linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64'].includes(target))) throw new Error('Unsupported or duplicate CLI target')
  const output = await canonicalOutput(resolve(options['--out-dir']))
  if (output === repo || output.startsWith(`${repo}/`)) throw new Error('Output must be outside the source checkout')
  try { await access(output); throw new Error('Output directory already exists') } catch (error) { if (error.code !== 'ENOENT') throw error }
  if (run('git', ['status', '--porcelain', '--untracked-files=all'], { cwd: repo }).trim()) throw new Error('Commit source changes before packaging; bundles build an immutable Git snapshot')
  const commit = run('git', ['rev-parse', 'HEAD'], { cwd: repo }).trim()
  const epoch = Number(run('git', ['show', '-s', '--format=%ct', commit], { cwd: repo }).trim())
  const tools = { go: run('go', ['env', 'GOVERSION']).trim(), node: process.versions.node, npm: run('npm', ['--version']).trim() }
  const pinnedGo = pinnedGoToolchain(await readFile(join(repo, 'go.mod'), 'utf8'))
  if (tools.go !== `go${pinnedGo}` || Number(tools.node.split('.')[0]) < 22) throw new Error('Use the repository-pinned Go toolchain and Node.js 22 or newer')
  await mkdir(dirname(output), { recursive: true })
  const work = await mkdtemp(join(tmpdir(), 'gregale-data-bundle-'))
  const stage = await mkdtemp(join(dirname(output), '.gregale-data-bundle-'))
  try {
    const source = join(work, 'source')
    await mkdir(source)
    run('git', ['archive', '--format=tar', '-o', join(work, 'source.tar'), commit], { cwd: repo })
    run('tar', ['-xf', join(work, 'source.tar'), '-C', source])
    const defaultAction = (await readFile(join(source, 'cmd/gregale/cmd_deploy_github.go'), 'utf8')).match(/var githubActionDefaultSHA = "([a-f0-9]{40})"/)?.[1]
    const actionSHA = options['--action-sha'] || defaultAction
    if (!/^[a-f0-9]{40}$/.test(actionSHA)) throw new Error('A full deploy Action SHA is required')
    const sdkDir = join(source, 'sdk/data')
    run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: sdkDir, stdio: 'inherit' })
    run('npm', ['run', 'build'], { cwd: sdkDir, stdio: 'inherit' })
    const packed = JSON.parse(run('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', stage], { cwd: sdkDir }))[0]
    const descriptor = async file => {
      const data = await readFile(join(stage, file))
      return { file, bytes: data.length, sha256: createHash('sha256').update(data).digest('hex') }
    }
    const sdk = { ...await descriptor(packed.filename), name: packed.name, version: packed.version, integrity: packed.integrity }
    const archives = []
    for (const target of targets) {
      const [os, arch] = target.split('/')
      const binary = join(work, `gregale-${os}-${arch}`)
      const buildTime = new Date(epoch * 1000).toISOString().replace('.000Z', 'Z')
      const wire = 'github.com/onebox-faas/faas/pkg/wire'
      run('go', ['build', '-trimpath', '-buildvcs=false', '-ldflags',
        `-s -w -X ${wire}.Version=${version} -X ${wire}.GitSHA=${commit} -X ${wire}.BuildTime=${buildTime} -X main.githubActionDefaultSHA=${actionSHA}`,
        '-o', binary, './cmd/gregale'], {
        cwd: source, timeout: 15 * 60 * 1000, stdio: 'inherit',
        env: { ...process.env, CGO_ENABLED: '0', GOOS: os, GOARCH: arch, GOAMD64: 'v1', GOARM64: 'v8.0', GOEXPERIMENT: '', GOTOOLCHAIN: 'local', GOWORK: 'off', GOFLAGS: '-mod=readonly "-gcflags=github.com/onebox-faas/faas/cmd/gregale=-dwarf=false -c=1"' },
      })
      const file = `gregale_${version.slice(1)}_${os}_${arch}.tar.gz`
      run('bash', [join(source, 'scripts/archive-cli-binary.sh'), '--binary', binary, '--output', join(stage, file), '--mtime', `@${epoch}`], { stdio: 'inherit' })
      archives.push({ ...await descriptor(file), os, arch })
    }
    const bundle = validateBundle({ format: 1, contract: 1, source: { commit, epoch }, tools, cli: { version, action_sha: actionSHA, archives }, sdk })
    await writeFile(join(stage, 'data-api-bundle.json'), `${JSON.stringify(bundle, null, 2)}\n`)
    const files = [...archives.map(entry => entry.file), sdk.file, 'data-api-bundle.json'].sort()
    const sums = await Promise.all(files.map(async file => `${(await descriptor(file)).sha256}  ${file}\n`))
    await writeFile(join(stage, 'DATA-API-SHA256SUMS'), sums.join(''))
    await rename(stage, output)
    console.log(`Data API bundle: ${output}`)
  } finally {
    await rm(work, { recursive: true, force: true })
    await rm(stage, { recursive: true, force: true })
  }
}

try { await main(process.argv.slice(2)) } catch (error) {
  console.error(`Data API bundle: ${error.message}`)
  process.exitCode = 1
}
