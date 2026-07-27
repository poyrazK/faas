// test/helpers/spawn-fakeapid.ts — spawn the Go fixture for the
// smoke test.
//
// Mirrors `sdk/fakeapid/main_test.go::spawnFakeAPID`. On Linux/macOS
// we set `detached:true` so the spawned process gets its own process
// group; the stop function then sends SIGKILL to `-pid` (mirroring
// Go's `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)`). On Windows
// `detached:true` is meaningless and `process.kill(-pid)` is not
// implemented, so we fall back to a plain `kill(pid)`.
//
// Default `binPath` is `../fakeapid/bin/fakeapid` (relative to the
// package root). The Makefile's `sdk-smoke-node` target builds the
// binary at that path before invoking this helper.

import { spawn, type ChildProcess } from 'node:child_process';
import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

import { freePort } from './free-port.js';

// `import.meta.dirname` is stable on Node 22.10+ (the SDK's
// minimum version). It resolves to the directory of the file
// currently being executed, which is always the compiled `.js`
// output at `<pkg>/dist-test/test/helpers/`. We walk up three
// levels to reach the package root: `helpers/` → `test/` →
// `dist-test/` → `<pkg>/`. Then walk up one more to the repo's
// `sdk/` parent where the fakeapid fixture lives.
const here = import.meta.dirname;
const pkgRoot = resolve(here, '..', '..', '..');
const defaultBin = resolve(pkgRoot, '..', 'fakeapid', 'bin', 'fakeapid');

const isWindows = process.platform === 'win32';

export interface SpawnedFakeApid {
  /** Base URL of the running fixture (e.g. `http://127.0.0.1:8123`). */
  baseURL: string;
  /** Stop function — kills the subprocess + awaits its exit. */
  stop: () => Promise<void>;
}

/**
 * Start `binPath` (or the default `./sdk/fakeapid/bin/fakeapid`) on
 * a free port and wait for `/__healthz` to return 200 OK. Resolves
 * once the fixture is ready; rejects after `timeoutMs` (default 5s,
 * same as the Go helper).
 *
 * The fixture is built at `binPath`; callers should ensure it's
 * present before invoking. The helper refuses to start without it.
 */
export async function spawnFakeApid(opts: {
  binPath?: string;
  timeoutMs?: number;
} = {}): Promise<SpawnedFakeApid> {
  const bin = opts.binPath ?? defaultBin;
  const timeoutMs = opts.timeoutMs ?? 5_000;

  if (!existsSync(bin)) {
    throw new Error(
      `spawnFakeApid: binary not found at ${bin}. ` +
        `Build it with 'make -C sdk/fakeapid build' first.`,
    );
  }

  const port = await freePort();
  const child: ChildProcess = spawn(bin, [], {
    env: { ...process.env, PORT: String(port) },
    stdio: ['ignore', 'pipe', 'pipe'],
    // detached:true puts the child in its own process group so a
    // SIGKILL to -pid cleans up descendants. No-op on Windows.
    detached: !isWindows,
  });

  const baseURL = `http://127.0.0.1:${port}`;
  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(
        `spawnFakeApid: child exited early with code ${child.exitCode}`,
      );
    }
    try {
      const resp = await fetch(`${baseURL}/__healthz`);
      if (resp.status === 200) {
        // Drain the body so the connection closes cleanly.
        await resp.arrayBuffer();
        return {
          baseURL,
          stop: async () => {
            try {
              if (isWindows) {
                child.kill('SIGKILL');
              } else {
                // Kill the whole process group; detached:true
                // ensures the leader is the only thing we need
                // to signal.
                process.kill(-child.pid!, 'SIGKILL');
              }
            } catch {
              // Already dead.
            }
            // Wait for the child to fully exit so file handles
            // (port, log pipes) are released. If the kill didn't
            // take, surface a clear error.
            await new Promise<void>((resolve) => {
              if (child.exitCode !== null) {
                resolve();
                return;
              }
              child.once('exit', () => resolve());
              // Belt + suspenders: if the exit never fires, give
              // up after 2s.
              setTimeout(() => resolve(), 2_000);
            });
          },
        };
      }
    } catch {
      // Not ready yet — fall through to the next iteration.
    }
    await new Promise((r) => setTimeout(r, 50));
  }

  // Timed out — kill the child and report.
  try {
    if (isWindows) child.kill('SIGKILL');
    else process.kill(-child.pid!, 'SIGKILL');
  } catch { /* ignore */ }
  throw new Error(
    `spawnFakeApid: fixture did not become healthy on ${baseURL} within ${timeoutMs}ms`,
  );
}