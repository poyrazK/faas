// The managed PostgreSQL journey's app. It writes and reads rows through
// the connection URL a binding delivered, with a minimal PostgreSQL client
// (TLS, SCRAM-SHA-256, simple queries) so the build needs no dependencies.
//
//   POST /notes?env=KEY&body=TEXT  inserts a row, returns {body, at}
//   GET  /notes?env=KEY            returns {bodies, now}
//   node server.js migrate STEP    runs a release command (see migrations)
//
// KEY names the environment variable holding the URL (DATABASE_URL by
// default). TEXT is [a-z0-9-]{1,40}, so it can be quoted safely; writing the
// same TEXT again returns the existing row.

import crypto from "node:crypto";
import http from "node:http";
import net from "node:net";
import tls from "node:tls";

const port = Number(process.env.PORT || 8080);
const queryTimeoutMs = 20_000;

class Wire {
  constructor(socket) {
    this.socket = socket;
    this.buf = Buffer.alloc(0);
    this.waiter = null;
    this.err = null;
    socket.on("data", (d) => { this.buf = Buffer.concat([this.buf, d]); this.pump(); });
    socket.on("error", (e) => { this.err = e; this.pump(); });
    socket.on("close", () => { this.err ??= new Error("connection closed"); this.pump(); });
  }

  pump() {
    if (!this.waiter) return;
    if (this.buf.length >= 5) {
      const length = this.buf.readInt32BE(1);
      if (this.buf.length >= 1 + length) {
        const message = { type: String.fromCharCode(this.buf[0]), body: this.buf.subarray(5, 1 + length) };
        this.buf = this.buf.subarray(1 + length);
        const { resolve } = this.waiter;
        this.waiter = null;
        resolve(message);
        return;
      }
    }
    if (this.err) {
      const { reject } = this.waiter;
      this.waiter = null;
      reject(this.err);
    }
  }

  next() {
    return new Promise((resolve, reject) => { this.waiter = { resolve, reject }; this.pump(); });
  }

  send(type, body) {
    const head = Buffer.alloc(type ? 5 : 4);
    if (type) head.write(type, 0, "latin1");
    head.writeInt32BE(body.length + 4, type ? 1 : 0);
    this.socket.write(Buffer.concat([head, body]));
  }
}

const cstr = (s) => Buffer.from(s + "\0", "utf8");
const hmac = (key, data) => crypto.createHmac("sha256", key).update(data).digest();

function serverError(body) {
  const fields = {};
  for (let i = 0; i < body.length && body[i] !== 0;) {
    const code = String.fromCharCode(body[i]);
    const end = body.indexOf(0, i + 1);
    fields[code] = body.subarray(i + 1, end).toString("utf8");
    i = end + 1;
  }
  const err = new Error(fields.M || "server error");
  err.code = fields.C;
  return err;
}

async function startTLS(socket, url) {
  const mode = url.searchParams.get("sslmode") || "require";
  if (mode === "disable") return socket;
  const request = Buffer.alloc(8);
  request.writeInt32BE(8, 0);
  request.writeInt32BE(80877103, 4);
  socket.write(request);
  const answer = await new Promise((resolve, reject) => {
    socket.once("data", resolve);
    socket.once("error", reject);
  });
  if (answer[0] !== 0x53) throw new Error("server refused TLS");
  const secure = tls.connect({ socket, servername: url.hostname, rejectUnauthorized: mode.startsWith("verify") });
  await new Promise((resolve, reject) => {
    secure.once("secureConnect", resolve);
    secure.once("error", reject);
  });
  return secure;
}

async function authenticate(wire, user, password) {
  let nonce, clientFirstBare, serverSignature;
  for (;;) {
    const { type, body } = await wire.next();
    if (type === "E") throw serverError(body);
    if (type === "Z") return;
    if (type !== "R") continue;
    const code = body.readInt32BE(0);
    if (code === 0) continue;
    if (code === 3) {
      wire.send("p", cstr(password));
    } else if (code === 5) {
      const inner = crypto.createHash("md5").update(password + user).digest("hex");
      const outer = crypto.createHash("md5").update(Buffer.concat([Buffer.from(inner), body.subarray(4, 8)])).digest("hex");
      wire.send("p", cstr("md5" + outer));
    } else if (code === 10) {
      if (!body.subarray(4).toString("utf8").split("\0").includes("SCRAM-SHA-256")) throw new Error("server offers no SCRAM-SHA-256");
      nonce = crypto.randomBytes(18).toString("base64");
      clientFirstBare = `n=,r=${nonce}`;
      const first = Buffer.from("n,," + clientFirstBare);
      const length = Buffer.alloc(4);
      length.writeInt32BE(first.length);
      wire.send("p", Buffer.concat([cstr("SCRAM-SHA-256"), length, first]));
    } else if (code === 11) {
      const serverFirst = body.subarray(4).toString("utf8");
      const attrs = Object.fromEntries(serverFirst.split(",").map((kv) => [kv[0], kv.slice(2)]));
      if (!attrs.r?.startsWith(nonce)) throw new Error("SCRAM nonce mismatch");
      const salted = crypto.pbkdf2Sync(password, Buffer.from(attrs.s, "base64"), Number(attrs.i), 32, "sha256");
      const clientKey = hmac(salted, "Client Key");
      const storedKey = crypto.createHash("sha256").update(clientKey).digest();
      const finalWithoutProof = `c=biws,r=${attrs.r}`;
      const authMessage = `${clientFirstBare},${serverFirst},${finalWithoutProof}`;
      const signature = hmac(storedKey, authMessage);
      const proof = Buffer.from(clientKey.map((b, i) => b ^ signature[i]));
      serverSignature = hmac(hmac(salted, "Server Key"), authMessage).toString("base64");
      wire.send("p", Buffer.from(`${finalWithoutProof},p=${proof.toString("base64")}`));
    } else if (code === 12) {
      if (body.subarray(4).toString("utf8") !== `v=${serverSignature}`) throw new Error("SCRAM server signature mismatch");
    } else {
      throw new Error(`unsupported authentication method ${code}`);
    }
  }
}

async function connect(rawURL) {
  const url = new URL(rawURL);
  const user = decodeURIComponent(url.username);
  const socket = net.connect(Number(url.port || 5432), url.hostname);
  await new Promise((resolve, reject) => {
    socket.once("connect", resolve);
    socket.once("error", reject);
  });
  const wire = new Wire(await startTLS(socket, url));
  const params = ["user", user, "database", decodeURIComponent(url.pathname.slice(1)), "client_encoding", "UTF8", "application_name", "gregale-journey"];
  const version = Buffer.alloc(4);
  version.writeInt32BE(196608);
  wire.send("", Buffer.concat([version, ...params.map(cstr), Buffer.from([0])]));
  await authenticate(wire, user, decodeURIComponent(url.password));
  return wire;
}

// query runs one simple-protocol statement list and returns the last
// result's rows as arrays of strings (null for NULL).
async function query(rawURL, sql) {
  const wire = await connect(rawURL);
  const timer = setTimeout(() => wire.socket.destroy(new Error("query timed out")), queryTimeoutMs);
  try {
    wire.send("Q", cstr(sql));
    let rows = [], failure = null;
    for (;;) {
      const { type, body } = await wire.next();
      if (type === "T") rows = [];
      else if (type === "D") {
        const row = [];
        for (let i = 0, at = 2, n = body.readInt16BE(0); i < n; i++) {
          const length = body.readInt32BE(at);
          at += 4;
          row.push(length < 0 ? null : body.subarray(at, at + length).toString("utf8"));
          if (length > 0) at += length;
        }
        rows.push(row);
      } else if (type === "E") failure = serverError(body);
      else if (type === "Z") {
        if (failure) throw failure;
        return rows;
      }
    }
  } finally {
    clearTimeout(timer);
    wire.send("X", Buffer.alloc(0));
    wire.socket.end();
  }
}

const schema = "CREATE TABLE IF NOT EXISTS journey_notes (id bigserial PRIMARY KEY, body text NOT NULL UNIQUE, at timestamptz NOT NULL DEFAULT clock_timestamp());";
const utc = (column) => `to_char(${column} AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`;

async function notes(req, url) {
  const key = url.searchParams.get("env") || "DATABASE_URL";
  if (!/^[A-Z][A-Z0-9_]*$/.test(key)) return [400, { error: "env must name an environment variable" }];
  const database = process.env[key];
  if (!database) return [503, { error: `${key} is not set` }];
  if (req.method === "POST") {
    const body = url.searchParams.get("body") || "";
    if (!/^[a-z0-9-]{1,40}$/.test(body)) return [400, { error: "body must match [a-z0-9-]{1,40}" }];
    // A retried write returns the first attempt's row, so a lost response
    // never adds a second one.
    const rows = await query(database, `${schema} INSERT INTO journey_notes (body) VALUES ('${body}') ON CONFLICT (body) DO UPDATE SET body = EXCLUDED.body RETURNING body, ${utc("at")};`);
    return [200, { body: rows[0][0], at: rows[0][1] }];
  }
  const rows = await query(database, `${schema} SELECT body, ${utc("clock_timestamp()")} FROM journey_notes ORDER BY id;`);
  const now = await query(database, `SELECT ${utc("clock_timestamp()")};`);
  return [200, { bodies: rows.map((row) => row[0]), now: now[0][0] }];
}

// migrations are the database rollback journey's release commands
// (TestManagedPostgresRollbackRestore). They connect through the migration
// binding, MIGRATION_DATABASE_URL, which only the release task receives.
const migrations = {
  create: schema,
  // The bad migration the journey rolls back: it deletes every row.
  wipe: `${schema} DELETE FROM journey_notes;`,
};

async function migrate(step) {
  const database = process.env.MIGRATION_DATABASE_URL;
  if (!database) throw new Error("MIGRATION_DATABASE_URL is not set");
  if (!migrations[step]) throw new Error(`unknown migration ${step}`);
  await query(database, migrations[step]);
  console.log(`migration ${step} done`);
}

if (process.argv[2] === "migrate") {
  migrate(process.argv[3]).catch((err) => {
    console.error(`migration ${process.argv[3]} failed: ${err.message}`);
    process.exit(1);
  });
} else {
  serve();
}

function serve() {
  http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://probe");
    let status = 200, payload = { ok: true };
    if (url.pathname === "/notes") {
      try {
        [status, payload] = await notes(req, url);
      } catch (err) {
        status = 502;
        payload = { error: err.message, code: err.code || "" };
      }
    } else if (url.pathname !== "/" && url.pathname !== "/healthz") {
      status = 404;
      payload = { error: "not found" };
    }
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify(payload) + "\n");
  }).listen(port, () => console.log(`postgres probe listening on ${port}`));
}
