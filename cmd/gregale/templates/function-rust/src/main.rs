// Function template for gregale's binary runtime (ADR-987).
//
// The binary runtime runs one static Linux executable. The runner listens on
// :8080 and talks to this program over stdin/stdout with newline-framed §4.9
// envelopes: one request envelope per line in, one response envelope per line
// out. stdout carries only those envelopes, so log with eprintln!; stderr
// reaches `gregale logs`.
//
// The program speaks the persistent protocol: the runner starts it once per
// instance (and again after a snapshot restore) and sends every request to the
// same process, so setup done before the loop is paid once.

use std::io::{self, BufRead, Write};

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use serde::{Deserialize, Serialize};

/// The runner looks for these bytes in the compiled executable to know it keeps
/// serving after the first request; the handshake below keeps them referenced.
const PERSISTENT_PROTOCOL: &str = "FAAS_PERSISTENT_PROTOCOL_V1";

/// The §4.9 request envelope.
#[derive(Deserialize, Default)]
#[serde(default)]
struct Envelope {
    method: String,
    path: String,
    query: String,
    body_b64: String,
}

/// The §4.9 response envelope.
#[derive(Serialize)]
struct Response {
    status: u16,
    headers: std::collections::BTreeMap<String, String>,
    body_b64: String,
}

#[derive(Serialize)]
struct Reply<'a> {
    ok: bool,
    method: &'a str,
    path: &'a str,
    query: &'a str,
    body_bytes: usize,
}

/// handle is your function. It runs once per request.
fn handle(env: &Envelope) -> Response {
    let body_bytes = STANDARD.decode(&env.body_b64).map(|b| b.len()).unwrap_or(0);
    let reply = Reply { ok: true, method: &env.method, path: &env.path, query: &env.query, body_bytes };
    let body = serde_json::to_vec(&reply).unwrap_or_default();
    let mut headers = std::collections::BTreeMap::new();
    headers.insert("content-type".to_string(), "application/json".to_string());
    Response { status: 200, headers, body_b64: STANDARD.encode(body) }
}

fn error_response(status: u16) -> Response {
    Response { status, headers: Default::default(), body_b64: String::new() }
}

fn main() -> io::Result<()> {
    let stdout = io::stdout();
    let mut out = stdout.lock();
    if std::env::var("FAAS_PERSISTENT_WORKER").as_deref() == Ok("1") {
        let ready = serde_json::json!({ "__faas_ready": true, "protocol": PERSISTENT_PROTOCOL });
        writeln!(out, "{ready}")?;
        out.flush()?;
    }
    for line in io::stdin().lock().lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        let response = match serde_json::from_str::<Envelope>(&line) {
            Ok(env) => handle(&env),
            Err(err) => {
                eprintln!("function-rust: decode envelope: {err}");
                error_response(500)
            }
        };
        serde_json::to_writer(&mut out, &response)?;
        out.write_all(b"\n")?;
        out.flush()?;
    }
    Ok(())
}
