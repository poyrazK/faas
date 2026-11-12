#!/usr/bin/env node
// gregale-browser — the sandbox browser (ADR-20261112121456 phase 2,
// ADR-20261027090014 §4.3). It starts headless Chromium with its DevTools
// protocol (CDP) on 127.0.0.1:9221 and serves CDP on 127.0.0.1:9222 through a
// relay, the port a session exposes at its preview URL.
//
// The relay exists because a preview request keeps its preview host as Host,
// and Chromium refuses DevTools requests whose Host is not localhost or an
// address. The relay sends Chromium Host: 127.0.0.1:9221 and rewrites the
// WebSocket URLs Chromium answers with to the caller's host, so
// `chromium.connectOverCDP(previewURL, {headers})` works from outside.
//
// Usage:
//   gregale-browser [-- chromium flags...]   start; runs until Chromium exits
//   gregale-browser --wait [seconds]         exit 0 once CDP answers
//
// Only Node built-ins: it runs from the read-only agent-base drive0.
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import http from 'node:http';
import net from 'node:net';

const RELAY_PORT = Number(process.env.GREGALE_BROWSER_PORT || 9222);
const CDP_PORT = RELAY_PORT - 1;
const CDP_HOST = `127.0.0.1:${CDP_PORT}`;

// isLocal reports whether a Host header names this machine, so answers keep
// plain ws:// for callers inside the sandbox.
export function isLocal(host) {
  const name = (host || '').replace(/:\d+$/, '').replace(/^\[|\]$/g, '');
  return name === 'localhost' || name === '127.0.0.1' || name === '::1';
}

// rewriteBody points the WebSocket URLs in a /json answer at the caller's
// host: wss:// through a preview URL, ws:// inside the sandbox.
export function rewriteBody(body, host) {
  if (!host) {
    return body;
  }
  const scheme = isLocal(host) ? 'ws' : 'wss';
  return body.split(`ws://${CDP_HOST}`).join(`${scheme}://${host}`).split(`ws=${CDP_HOST}`).join(`${scheme}=${host}`);
}

// upgradeHead is the WebSocket upgrade request sent to Chromium: the
// caller's request with Host set to Chromium's and Origin dropped.
export function upgradeHead(req) {
  const lines = [`${req.method} ${req.url} HTTP/1.1`, `Host: ${CDP_HOST}`];
  for (let i = 0; i < req.rawHeaders.length; i += 2) {
    const name = req.rawHeaders[i].toLowerCase();
    if (name !== 'host' && name !== 'origin') {
      lines.push(`${req.rawHeaders[i]}: ${req.rawHeaders[i + 1]}`);
    }
  }
  return lines.join('\r\n') + '\r\n\r\n';
}

// relay serves CDP on RELAY_PORT, forwarding to Chromium on CDP_PORT.
export function relay() {
  const server = http.createServer((req, res) => {
    const headers = { ...req.headers, host: CDP_HOST };
    delete headers.origin;
    const up = http.request({ host: '127.0.0.1', port: CDP_PORT, method: req.method, path: req.url, headers }, (cdp) => {
      if (!req.url.startsWith('/json')) {
        res.writeHead(cdp.statusCode, cdp.headers);
        cdp.pipe(res);
        return;
      }
      const chunks = [];
      cdp.on('data', (c) => chunks.push(c));
      cdp.on('end', () => {
        const body = Buffer.from(rewriteBody(Buffer.concat(chunks).toString('utf8'), req.headers.host), 'utf8');
        const out = { ...cdp.headers, 'content-length': String(body.length) };
        delete out['transfer-encoding'];
        res.writeHead(cdp.statusCode, out);
        res.end(body);
      });
    });
    up.on('error', () => {
      res.writeHead(502, { 'content-type': 'text/plain' });
      res.end('gregale-browser: Chromium is not answering\n');
    });
    req.pipe(up);
  });
  server.on('upgrade', (req, socket, head) => {
    const cdp = net.connect(CDP_PORT, '127.0.0.1', () => {
      cdp.write(upgradeHead(req));
      if (head.length > 0) {
        cdp.write(head);
      }
      cdp.pipe(socket);
      socket.pipe(cdp);
    });
    const close = () => {
      cdp.destroy();
      socket.destroy();
    };
    cdp.on('error', close);
    socket.on('error', close);
  });
  server.listen(RELAY_PORT, '127.0.0.1');
  return server;
}

function chromiumPath() {
  for (const p of [process.env.GREGALE_BROWSER_CHROMIUM, '/usr/bin/chromium', '/usr/bin/chromium-browser']) {
    if (existsSync(p)) {
      return p;
    }
  }
  throw new Error('gregale-browser: no Chromium in this image');
}

function versionOK(port) {
  return new Promise((resolve) => {
    const req = http.get({ host: '127.0.0.1', port, path: '/json/version', timeout: 1000 }, (res) => {
      res.resume();
      resolve(res.statusCode === 200);
    });
    req.on('error', () => resolve(false));
    req.on('timeout', () => {
      req.destroy();
      resolve(false);
    });
  });
}

async function waitFor(port, seconds) {
  const deadline = Date.now() + seconds * 1000;
  while (Date.now() < deadline) {
    if (await versionOK(port)) {
      return true;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  return false;
}

async function start(extra) {
  const profile = process.env.GREGALE_BROWSER_PROFILE || '/tmp/gregale-browser';
  mkdirSync(profile, { recursive: true });
  // --no-sandbox: the microVM is the sandbox, and the guest has no user
  // namespaces for Chromium's own.
  const args = ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run',
    '--no-default-browser-check', '--remote-debugging-address=127.0.0.1', `--remote-debugging-port=${CDP_PORT}`,
    '--remote-allow-origins=*', `--user-data-dir=${profile}`, ...extra, 'about:blank'];
  const chromium = spawn(chromiumPath(), args, { stdio: ['ignore', 'inherit', 'inherit'] });
  chromium.on('exit', (code, signal) => process.exit(code ?? (signal ? 1 : 0)));
  for (const sig of ['SIGINT', 'SIGTERM']) {
    process.on(sig, () => chromium.kill(sig));
  }
  if (!(await waitFor(CDP_PORT, 30))) {
    chromium.kill('SIGKILL');
    console.error('gregale-browser: Chromium did not answer on its DevTools port within 30 s');
    process.exit(1);
  }
  relay();
  console.log(`gregale-browser: CDP at http://127.0.0.1:${RELAY_PORT}`);
}

async function main(argv) {
  if (argv[0] === '--wait') {
    const seconds = Number(argv[1] || 30);
    process.exit((await waitFor(RELAY_PORT, seconds)) ? 0 : 1);
  }
  const dash = argv.indexOf('--');
  await start(dash >= 0 ? argv.slice(dash + 1) : []);
}

if (import.meta.url === `file://${process.argv[1]}` || process.argv[1]?.endsWith('/gregale-browser')) {
  main(process.argv.slice(2)).catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
