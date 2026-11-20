// The managed Redis journey's app. It runs Redis commands against the
// cache a binding delivered, with a minimal RESP client so the build needs
// no dependencies.
//
//   GET /redis?cmd=SET%20k%20v&cmd=GET%20k  returns {replies: [...]}
//
// Each cmd is split on spaces and sent as one RESP array over a fresh
// connection to REDIS_URL (redis://host:port); each reply comes back as its
// raw RESP text ("+OK\r\n", "$1\r\nv\r\n"), so the journey checks exactly
// what the cache answered. The connection closes after the replies, so the
// app never keeps the cache awake.

import http from "node:http";
import net from "node:net";

const port = Number(process.env.PORT || 8080);
const replyTimeoutMs = 60_000;

// replyLength is the byte length of the first complete RESP reply in buf,
// or -1 while it is incomplete.
function replyLength(buf, at = 0) {
  const eol = buf.indexOf("\r\n", at);
  if (eol < 0) return -1;
  const type = String.fromCharCode(buf[at]);
  const head = eol + 2;
  if (type === "+" || type === "-" || type === ":") return head - at;
  const n = Number(buf.toString("latin1", at + 1, eol));
  if (type === "$") {
    if (n < 0) return head - at;
    return buf.length >= head + n + 2 ? head + n + 2 - at : -1;
  }
  if (type === "*") {
    let end = head;
    for (let i = 0; i < Math.max(n, 0); i++) {
      const len = replyLength(buf, end);
      if (len < 0) return -1;
      end += len;
    }
    return end - at;
  }
  throw new Error("unexpected RESP type " + JSON.stringify(type));
}

function encode(cmd) {
  const args = cmd.split(" ").filter((a) => a !== "");
  let out = `*${args.length}\r\n`;
  for (const a of args) out += `$${Buffer.byteLength(a)}\r\n${a}\r\n`;
  return out;
}

function run(cmds) {
  const target = new URL(process.env.REDIS_URL || "");
  return new Promise((resolve, reject) => {
    const socket = net.connect({ host: target.hostname, port: Number(target.port || 6379) });
    let buf = Buffer.alloc(0);
    const replies = [];
    const timer = setTimeout(() => socket.destroy(new Error("no reply within 60 s")), replyTimeoutMs);
    socket.on("connect", () => socket.write(cmds.map(encode).join("")));
    socket.on("data", (d) => {
      buf = Buffer.concat([buf, d]);
      for (;;) {
        const len = replyLength(buf);
        if (len < 0) break;
        replies.push(buf.toString("latin1", 0, len));
        buf = buf.subarray(len);
      }
      if (replies.length === cmds.length) {
        clearTimeout(timer);
        socket.end();
        resolve(replies);
      }
    });
    socket.on("error", (e) => { clearTimeout(timer); reject(e); });
    socket.on("close", () => {
      clearTimeout(timer);
      if (replies.length < cmds.length) reject(new Error(`connection closed after ${replies.length} of ${cmds.length} replies`));
    });
  });
}

http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://probe");
  const send = (status, body) => {
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify(body));
  };
  if (url.pathname === "/") return send(200, { ok: true, bound: Boolean(process.env.REDIS_URL) });
  if (url.pathname !== "/redis") return send(404, { error: "not found" });
  const cmds = url.searchParams.getAll("cmd");
  if (cmds.length === 0) return send(400, { error: "give at least one cmd" });
  if (!process.env.REDIS_URL) return send(503, { error: "REDIS_URL is not set" });
  try {
    send(200, { replies: await run(cmds) });
  } catch (e) {
    send(502, { error: String(e.message || e) });
  }
}).listen(port);
