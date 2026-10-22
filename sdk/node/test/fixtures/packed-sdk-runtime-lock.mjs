import {createHash} from 'node:crypto';
import {readFileSync, writeFileSync} from 'node:fs';
import {join, resolve} from 'node:path';

// npm ci caches locked tarballs, not necessarily registry metadata. Reuse
// those exact runtime pins so packed-SDK starter installation stays offline.
export function writePackedSDKRuntimeLock(directory, sdk) {
  const manifest = JSON.parse(readFileSync(join(directory, 'package.json'), 'utf8'));
  const sdkManifest = JSON.parse(readFileSync(join(sdk, 'package.json'), 'utf8'));
  const sdkLock = JSON.parse(readFileSync(join(sdk, 'package-lock.json'), 'utf8'));
  const archive = manifest.dependencies[sdkManifest.name];
  if (!archive?.startsWith('file:')) throw new Error('starter must install the real packed SDK');
  const integrity = 'sha512-' + createHash('sha512')
    .update(readFileSync(resolve(directory, archive.slice(5)))).digest('base64');
  const packages = {
    '': {name: manifest.name, version: manifest.version, dependencies: manifest.dependencies},
    [`node_modules/${sdkManifest.name}`]: {
      version: sdkManifest.version, resolved: archive, integrity,
      dependencies: sdkManifest.dependencies,
      optionalDependencies: sdkManifest.optionalDependencies,
      engines: sdkManifest.engines,
      bin: sdkManifest.bin,
    },
  };
  for (const [path, entry] of Object.entries(sdkLock.packages)) {
    if (path && !entry.dev) packages[path] = entry;
  }
  writeFileSync(join(directory, 'package-lock.json'), JSON.stringify({
    name: manifest.name, version: manifest.version, lockfileVersion: 3,
    requires: true, packages,
  }, null, 2) + '\n');
}
