'use strict';
// Gregale restore reseed preload (GHSA-24j2-p895-mwc9, ADR-687).
//
// guest-init injects this file with NODE_OPTIONS=--require. A snapshot
// restores a Node process with the random state it held at capture, so every
// restore of one snapshot would replay the same crypto.randomBytes, TLS
// randomness, randomUUID/randomInt values and Math.random sequence. On resume,
// guest-init sends each registered process a fresh nonce; this preload then
// reseeds OpenSSL (reseed.node -> RAND_poll), reseeds Math.random and marks
// Node's userspace crypto caches stale before replying "ok", so the first call
// that reads a cache drains it (ADR-981). guest-init waits for that reply
// before the instance can serve traffic.
//
// With GREGALE_RESEED_MODE=thread (ADR-20261024105607) the addon answers
// the barrier from its own native thread instead: RAND_poll(), then a bump
// of the process-wide restore generation, then "ok", without waking the event
// loop. Math.random and the cache wrappers check that generation before they
// return a value and adopt a new one first; the addon also wakes the event
// loop to adopt it right away, alongside the platform's readiness work.
//
// It must never write to stdout (function adapters frame responses there) and
// must never keep the event loop alive.
(function gregaleRestoreReseed() {
  const socketPath = process.env.GREGALE_RESEED_SOCKET;
  if (!socketPath || String(process.env.GREGALE_RESTORE_RESEED).toLowerCase() === 'off') return;
  // Package-manager launchers (npm, npx, yarn, pnpm, corepack) only start the
  // app as a child process and wait for it. They serve no requests and every
  // process they start later is a fresh exec the kernel seeds, so their
  // replayed generators are never observable. Registering them made every
  // `npm run start` restore wait for npm's large idle heap to fault back in:
  // resume_hook p50 111 -> 156 ms, p90 248 ms on the metal bench (hunt #9).
  if (isPackageManagerLauncher(process.argv[1])) return;
  const marker = Symbol.for('gregale.restoreReseed');
  if (globalThis[marker]) return;
  const exposed = { state: null };
  globalThis[marker] = exposed;

  let net;
  let crypto;
  let fs;
  let path;
  try {
    net = require('node:net');
    crypto = require('node:crypto');
    fs = require('node:fs');
    path = require('node:path');
  } catch (_) {
    return;
  }

  let addon = null;
  try {
    addon = require(path.join(__dirname, 'reseed.node'));
  } catch (_) {
    addon = null; // reported as a failed reseed on resume, never at boot
  }

  // Thread mode: generation[0] is the process-wide restore generation the
  // addon's thread bumps after RAND_poll() and before it answers the barrier;
  // generation[1] is 1 while that thread is registered.
  let generation = null;
  if (String(process.env.GREGALE_RESEED_MODE).toLowerCase() === 'thread' && addon &&
      typeof addon.shared === 'function' && typeof addon.start === 'function') {
    try {
      const words = addon.shared();
      if (words instanceof ArrayBuffer) generation = new Int32Array(words, 0, 2);
    } catch (_) {
      generation = null; // the event-loop client below registers instead
    }
  }
  let adoptedGeneration = generation === null ? 0 : Atomics.load(generation, 0);

  // Math.random: V8 keeps its generator state in the isolate and exposes no
  // way to reseed it, so replace it with xoshiro128** that can be reseeded.
  const state = new Uint32Array(4);
  exposed.state = state; // live view of the generator, for tests and diagnostics
  const rotl = (x, k) => (x << k) | (x >>> (32 - k));
  function seedMathRandom(bytes) {
    for (let i = 0; i < 4; i++) state[i] = bytes.readUInt32LE(i * 4);
    if ((state[0] | state[1] | state[2] | state[3]) === 0) state[0] = 1;
  }
  function next32() {
    const s = state;
    const result = Math.imul(rotl(Math.imul(s[1], 5), 7), 9) >>> 0;
    const t = s[1] << 9;
    s[2] ^= s[0];
    s[3] ^= s[1];
    s[1] ^= s[2];
    s[0] ^= s[3];
    s[2] ^= t;
    s[3] = rotl(s[3], 11);
    return result;
  }
  seedMathRandom(crypto.randomBytes(16));
  const random = function random() {
    if (generation !== null && Atomics.load(generation, 0) !== adoptedGeneration) adoptRestore();
    return ((next32() >>> 5) * 67108864 + (next32() >>> 6)) / 9007199254740992;
  };
  Object.defineProperty(Math, 'random', { value: random, writable: true, configurable: true, enumerable: false });

  function freshBytes(nonce) {
    const out = Buffer.alloc(16);
    try {
      const fd = fs.openSync('/dev/urandom', 'r');
      try {
        fs.readSync(fd, out, 0, out.length, null);
      } finally {
        fs.closeSync(fd);
      }
    } catch (_) {
      // The nonce alone is fresh: guest-init draws it after the kernel reseed.
    }
    for (let i = 0; i < out.length; i++) out[i] ^= nonce[i % nonce.length];
    return out;
  }

  // Node serves randomUUID (crypto's and webcrypto's) from one 128-entry
  // batch and randomInt from a 6 KiB buffer, both filled before the
  // snapshot. Consuming a full batch crosses exactly one refill, so after a
  // reseed it leaves only entries drawn from the reseeded OpenSSL. A restore
  // marks both caches stale and the first call that reads one drains it
  // first (ADR-981): an app that never calls them never pays the drain.
  const stale = { uuid: false, int: false };
  exposed.stale = stale;

  // adoptRestore applies a restore the addon's thread already confirmed:
  // OpenSSL is reseeded, so randomBytes draws fresh bytes for Math.random and
  // a cache drain refills from the reseeded generator.
  function adoptRestore() {
    if (generation === null) return;
    const current = Atomics.load(generation, 0);
    if (current === adoptedGeneration) return;
    adoptedGeneration = current;
    seedMathRandom(crypto.randomBytes(16));
    stale.uuid = true;
    stale.int = true;
  }
  exposed.adoptRestore = adoptRestore;
  const nodeRandomUUID = crypto.randomUUID;
  const nodeRandomInt = crypto.randomInt;
  function drainUUIDs() {
    adoptRestore();
    if (!stale.uuid) return;
    stale.uuid = false;
    for (let i = 0; i < 128; i++) nodeRandomUUID();
  }
  function drainInts() {
    adoptRestore();
    if (!stale.int) return;
    stale.int = false;
    // A range of 1 accepts every 6-byte entry and returns 0, so each call
    // consumes one entry without allocating a heap number.
    for (let i = 0; i < 1024; i++) nodeRandomInt(0, 1);
  }
  function wrap(original, before) {
    const wrapped = {
      [original.name](...args) {
        before();
        return Reflect.apply(original, this, args);
      },
    }[original.name];
    Object.defineProperty(wrapped, 'length', { value: original.length });
    return wrapped;
  }
  crypto.randomUUID = wrap(nodeRandomUUID, drainUUIDs);
  crypto.randomInt = wrap(nodeRandomInt, drainInts);
  try {
    const proto = Object.getPrototypeOf(crypto.webcrypto);
    const desc = Object.getOwnPropertyDescriptor(proto, 'randomUUID');
    if (desc && typeof desc.value === 'function') {
      Object.defineProperty(proto, 'randomUUID', { ...desc, value: wrap(desc.value, drainUUIDs) });
    }
  } catch (_) {
    // No webcrypto in this build: nothing else reads the batch.
  }

  function reseed(nonceHex) {
    const nonce = Buffer.from(nonceHex, 'hex');
    if (nonce.length < 16) return 'err bad_nonce';
    if (!addon || typeof addon.poll !== 'function') return 'err openssl_reseed_unavailable';
    if (addon.poll() !== true) return 'err openssl_reseed_failed';
    seedMathRandom(freshBytes(nonce));
    stale.uuid = true;
    stale.int = true;
    return 'ok';
  }

  if (generation !== null && startReseedThread()) return;

  let conn;
  try {
    conn = net.createConnection(socketPath);
  } catch (_) {
    return;
  }
  conn.unref();
  conn.setEncoding('utf8');
  conn.on('error', () => {}); // no guest-init socket: nothing to reseed against
  conn.on('connect', () => {
    conn.write(`hello node ${process.pid}\n`);
  });
  let pending = '';
  conn.on('data', (chunk) => {
    pending += chunk;
    let newline;
    while ((newline = pending.indexOf('\n')) >= 0) {
      const line = pending.slice(0, newline).trim();
      pending = pending.slice(newline + 1);
      if (!line.startsWith('reseed ')) continue;
      let reply;
      try {
        reply = reseed(line.slice('reseed '.length).trim());
      } catch (err) {
        reply = 'err ' + String((err && err.code) || (err && err.name) || 'exception').replace(/\s+/g, '_');
      }
      conn.write(reply + '\n');
    }
  });

  // startReseedThread registers through the addon's thread. Only the main
  // thread starts it: it answers for the whole process, and worker isolates
  // adopt each restore from the shared generation on first use. The pipe
  // only wakes the event loop to adopt a restore early.
  function startReseedThread() {
    let isMainThread = true;
    try {
      isMainThread = require('node:worker_threads').isMainThread;
    } catch (_) {
      isMainThread = true;
    }
    if (!isMainThread) {
      if (Atomics.load(generation, 1) === 1) return true;
      generation = null; // the main thread fell back: register this isolate itself
      return false;
    }
    let wakeFd = -1;
    try {
      wakeFd = addon.start(socketPath);
    } catch (_) {
      wakeFd = -1;
    }
    if (typeof wakeFd !== 'number' || wakeFd < 0) {
      generation = null;
      return false;
    }
    try {
      const wake = new net.Socket({ fd: wakeFd, readable: true, writable: false });
      wake.unref();
      wake.on('error', () => {});
      wake.on('data', adoptRestore);
    } catch (_) {
      // Lazy adoption on first use still holds without the early wake.
    }
    return true;
  }
})();

// isPackageManagerLauncher matches the entry script only when it resolves into
// a package manager's own install, so an application file that happens to be
// called npm.js still registers.
function isPackageManagerLauncher(main) {
  if (!main) return false;
  let real;
  try {
    real = require('node:fs').realpathSync(main);
  } catch (_) {
    return false;
  }
  return /[\\/]node_modules[\\/](npm[\\/]bin[\\/](npm|npx)-cli\.js|corepack[\\/]dist[\\/](yarn|yarnpkg|pnpm|pnpx|npm|npx|corepack)\.js|yarn[\\/]bin[\\/]yarn\.c?js|pnpm[\\/]bin[\\/](pnpm|pnpx)\.c?js)$/.test(real);
}
