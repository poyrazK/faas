#!/usr/bin/env node
import { constants, closeSync, openSync, writeFileSync } from 'node:fs';
import { buildIssueSourceMapManifest } from './issue-source-map-manifest.js';

const [directory, output, option, ...extra] = process.argv.slice(2);
if (!directory || !output || (option !== undefined && option !== '--replace') || extra.length) {
  console.error('Usage: gregale-issue-source-maps <bundle-directory> <manifest-output> [--replace]');
  process.exitCode = 1;
} else {
  try {
    const manifest = buildIssueSourceMapManifest(directory);
    // Repeated builds opt into replacing their generated output. Never follow
    // an output symlink; a failed discovery never touches the previous output.
    const flags = constants.O_WRONLY | constants.O_CREAT | constants.O_NOFOLLOW | constants.O_NONBLOCK |
      (option === '--replace' ? constants.O_TRUNC : constants.O_EXCL);
    const fd = openSync(output, flags, 0o644);
    try { writeFileSync(fd, manifest + '\n'); } finally { closeSync(fd); }
    console.log(`Issue source-map manifest written (${JSON.parse(manifest).artifacts.length} maps)`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : 'Could not build issue source-map manifest');
    process.exitCode = 1;
  }
}
