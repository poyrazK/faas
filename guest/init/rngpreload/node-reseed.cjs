'use strict';
// Gregale restore reseed preload (GHSA-24j2-p895-mwc9, ADR-481).
//
// guest-init injects this file with NODE_OPTIONS=--require. A snapshot
// restores a Node process with the random state it held at capture, so every
// restore of one snapshot would replay the same crypto.randomBytes, TLS
// randomness, randomUUID/randomInt values and Math.random sequence. On resume,
// guest-init sends each registered process a fresh nonce; this preload then
// reseeds OpenSSL (reseed.node -> RAND_poll), reseeds Math.random and drains
// Node's userspace crypto caches before replying "ok". guest-init waits for
// that reply before the instance can serve traffic.
//
// It must never write to stdout (function adapters frame responses there) and
// must never keep the event loop alive.
(function gregaleRestoreReseed() {
  const socketPath = process.env.GREGALE_RESEED_SOCKET;
  if (!socketPath || String(process.env.GREGALE_RESTORE_RESEED).toLowerCase() === 'off') return;
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

  function reseed(nonceHex) {
    const nonce = Buffer.from(nonceHex, 'hex');
    if (nonce.length < 16) return 'err bad_nonce';
    if (!addon || typeof addon.poll !== 'function') return 'err openssl_reseed_unavailable';
    if (addon.poll() !== true) return 'err openssl_reseed_failed';
    seedMathRandom(freshBytes(nonce));
    // Node serves randomUUID from a 128-entry batch and randomInt from a
    // 6 KiB buffer, both filled before the snapshot. Consuming a full batch
    // of each forces a refill from the now-reseeded OpenSSL.
    for (let i = 0; i < 128; i++) crypto.randomUUID();
    for (let i = 0; i < 1024; i++) crypto.randomInt(0, 0xffffffffff);
    return 'ok';
  }

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
})();
