// agent-infinity-rust: a stateless agent on Infinity's step mode
// (ADR-20261030125501). The realtime trigger runs this function once per
// agent-inbox message, and each run is one `AgentSystem::step`. Between
// messages the function parks and holds no RAM; the conversation is only
// its rows in your Postgres.
//
// Routes:
//   trigger record (POST)  one slice of the record's thread
//   GET /threads/:id       the thread, for your backend; needs
//                          Authorization: Bearer $AGENT_READ_TOKEN
//   /realtime/connect      refused: the inbox takes messages from your
//                          backend, never from browser clients
//
// The binary runtime (ADR-987) talks to this program over stdin/stdout: one
// request envelope per line in, one response envelope per line out. Log on
// stderr; it reaches `gregale logs`.

mod model;
#[cfg(test)]
mod pg_tests;
mod platform;
mod record;
mod store;
mod tools;

use std::collections::BTreeMap;
use std::io::{self, BufRead, Write};
use std::sync::Arc;

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use infinity_agent_core::ThreadId;
use infinity_agent_core::message::InfinityMessage;
use infinity_agent_core::system::{AgentSystemBuilder, EventCollector, NoDeferral};
use infinity_agent_core::tools::Tool;
use infinity_agent_core::traits::{ConversationStore, StateStore};
use rap_client::http::SimpleHttpClient;
use serde::{Deserialize, Serialize};

use platform::{GregaleSender, Platform};
use store::{Db, PgConversationStore, PgStateStore};

type BoxError = Box<dyn std::error::Error + Send + Sync>;

/// The runner looks for these bytes in the executable to keep one process
/// serving every request; the handshake keeps them referenced.
const PERSISTENT_PROTOCOL: &str = "FAAS_PERSISTENT_PROTOCOL_V1";

const PROMPT: &str =
    "You answer questions. Use the lookup tool for facts, and answer from its result.";

#[derive(Deserialize, Default)]
#[serde(default)]
struct Envelope {
    method: String,
    path: String,
    headers: BTreeMap<String, String>,
    body_b64: String,
}

#[derive(Serialize)]
struct Response {
    status: u16,
    headers: BTreeMap<String, String>,
    body_b64: String,
}

fn json(status: u16, value: serde_json::Value) -> Response {
    let mut headers = BTreeMap::new();
    headers.insert("content-type".to_owned(), "application/json".to_owned());
    Response {
        status,
        headers,
        body_b64: STANDARD.encode(value.to_string()),
    }
}

fn header<'a>(env: &'a Envelope, name: &str) -> Option<&'a str> {
    env.headers
        .iter()
        .find(|(k, _)| k.eq_ignore_ascii_case(name))
        .map(|(_, v)| v.as_str())
}

/// One slice's database connection. Managed Postgres URLs carry an sslmode;
/// anything but disable connects over TLS with verified certificates.
async fn connect() -> Result<Db, BoxError> {
    let url = std::env::var("DATABASE_URL")
        .map_err(|_| "DATABASE_URL is not set; attach a Postgres database")?;
    let (url, mode) = split_sslmode(&url);
    let mut config: tokio_postgres::Config = url.parse()?;
    let client = if mode.as_deref() == Some("disable") {
        let (client, conn) = config.connect(tokio_postgres::NoTls).await?;
        tokio::spawn(async move { drop(conn.await) });
        client
    } else {
        config.ssl_mode(tokio_postgres::config::SslMode::Require);
        let roots = rustls::RootCertStore {
            roots: webpki_roots::TLS_SERVER_ROOTS.to_vec(),
        };
        let tls = rustls::ClientConfig::builder_with_provider(Arc::new(
            rustls::crypto::ring::default_provider(),
        ))
        .with_safe_default_protocol_versions()?
        .with_root_certificates(roots)
        .with_no_client_auth();
        let (client, conn) = config
            .connect(tokio_postgres_rustls::MakeRustlsConnect::new(tls))
            .await?;
        tokio::spawn(async move { drop(conn.await) });
        client
    };
    let db = Db(Arc::new(client));
    db.ensure_schema().await?;
    Ok(db)
}

/// tokio-postgres does not parse libpq's verify-full or verify-ca, so the
/// mode is taken out of the URL and applied here.
fn split_sslmode(url: &str) -> (String, Option<String>) {
    let Some((base, query)) = url.split_once('?') else {
        return (url.to_owned(), None);
    };
    let mut mode = None;
    let kept: Vec<&str> = query
        .split('&')
        .filter(|pair| match pair.strip_prefix("sslmode=") {
            Some(m) => {
                mode = Some(m.to_owned());
                false
            }
            None => !pair.is_empty(),
        })
        .collect();
    let url = if kept.is_empty() {
        base.to_owned()
    } else {
        format!("{base}?{}", kept.join("&"))
    };
    (url, mode)
}

/// One slice: run the inbox message through `AgentSystem::step`, then close
/// the thread's callback URLs if it answered and waits on nothing.
async fn slice(
    platform: &Arc<Platform>,
    rec: record::Record,
) -> Result<serde_json::Value, BoxError> {
    let Some(input) = rec.input else {
        return Ok(serde_json::json!({ "ok": true, "display_only": true }));
    };
    let db = connect().await?;
    let sender = GregaleSender {
        platform: platform.clone(),
        endpoint_id: rec.endpoint_id.clone(),
    };
    let lookup = tools::LookupTool {
        platform: platform.clone(),
        endpoint_id: rec.endpoint_id.clone(),
        db: db.clone(),
        tool_url: std::env::var("TOOL_URL").ok().filter(|u| !u.is_empty()),
        http: SimpleHttpClient::new(),
    };
    let conversations = PgConversationStore(db.clone());
    let states = PgStateStore(db.clone());
    let mut system = AgentSystemBuilder::new(
        conversations.clone(),
        states.clone(),
        model::scripted(),
        sender,
    )
    .tools([Box::new(lookup) as Box<dyn Tool<GregaleSender>>])
    .extra_system_prompt(PROMPT)
    .build();
    let collector = EventCollector::new();
    let outcomes = system
        .step(
            vec![(input, rec.message_id.clone())],
            &collector,
            &mut NoDeferral,
        )
        .await?;
    let thread = ThreadId::from(rec.thread_id.clone());
    let history = conversations
        .load_history_up_to(&thread, None, None)
        .await?;
    let answered = matches!(history.last(), Some(InfinityMessage::Assistant { .. }));
    if answered && states.get_active_subscriptions(&thread).await?.is_empty() {
        platform
            .revoke_callbacks(&rec.endpoint_id, &rec.thread_id)
            .await?;
    }
    eprintln!(
        "{}",
        serde_json::json!({ "msg": "slice", "thread_id": rec.thread_id, "message_id": rec.message_id,
            "completed": outcomes.iter().any(|(_, o)| matches!(o, infinity_agent_core::system::StepOutcome::Completed { .. })),
            "events": collector.take().len(), "answered": answered })
    );
    Ok(serde_json::json!({ "ok": true }))
}

fn authorized(env: &Envelope) -> bool {
    let want = std::env::var("AGENT_READ_TOKEN").unwrap_or_default();
    let got = header(env, "authorization")
        .and_then(|v| v.strip_prefix("Bearer "))
        .unwrap_or("");
    // Constant time in the token's length.
    !want.is_empty()
        && got.len() == want.len()
        && got
            .bytes()
            .zip(want.bytes())
            .fold(0u8, |acc, (a, b)| acc | (a ^ b))
            == 0
}

fn thread_path(path: &str) -> Option<&str> {
    let id = path.strip_prefix("/threads/")?;
    let ok = !id.is_empty()
        && id.len() <= 64
        && id
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || b"._:-".contains(&c));
    ok.then_some(id)
}

async fn handle(platform: &Arc<Platform>, env: Envelope) -> Response {
    let path = env.path.split('?').next().unwrap_or("/").to_owned();
    if path.starts_with("/realtime/") {
        return if path == "/realtime/connect" {
            json(
                403,
                serde_json::json!({ "error": "the agent inbox takes no client connections" }),
            )
        } else {
            json(200, serde_json::json!({}))
        };
    }
    if env.method.eq_ignore_ascii_case("GET") {
        let Some(id) = thread_path(&path).filter(|_| authorized(&env)) else {
            return json(404, serde_json::json!({ "error": "not found" }));
        };
        return match async {
            connect()
                .await?
                .thread_view(id)
                .await
                .map_err(BoxError::from)
        }
        .await
        {
            Ok(view) => json(200, view),
            Err(e) => {
                eprintln!("read thread {id}: {e}");
                json(
                    500,
                    serde_json::json!({ "error": "the thread store is unavailable" }),
                )
            }
        };
    }
    let body = STANDARD.decode(&env.body_b64).unwrap_or_default();
    let Some(rec) = record::parse(&body) else {
        // Not an inbox message: complete it, since a retry cannot fix it.
        eprintln!("ignored a record that is not an agent-inbox message");
        return json(200, serde_json::json!({ "ignored": true }));
    };
    match slice(platform, rec).await {
        Ok(v) => json(200, v),
        // A failed slice is retried by the consumer group.
        Err(e) => {
            eprintln!("slice failed: {e}");
            json(500, serde_json::json!({ "error": "slice failed" }))
        }
    }
}

fn main() -> io::Result<()> {
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    let platform = Platform::from_env();
    let stdout = io::stdout();
    let mut out = stdout.lock();
    if std::env::var("FAAS_PERSISTENT_WORKER").as_deref() == Ok("1") {
        writeln!(
            out,
            "{}",
            serde_json::json!({ "__faas_ready": true, "protocol": PERSISTENT_PROTOCOL })
        )?;
        out.flush()?;
    }
    for line in io::stdin().lock().lines() {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        let response = match serde_json::from_str::<Envelope>(&line) {
            Ok(env) => runtime.block_on(handle(&platform, env)),
            Err(e) => {
                eprintln!("decode envelope: {e}");
                json(500, serde_json::json!({ "error": "bad envelope" }))
            }
        };
        serde_json::to_writer(&mut out, &response)?;
        out.write_all(b"\n")?;
        out.flush()?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sslmode_leaves_the_url() {
        assert_eq!(
            split_sslmode("postgres://u:p@h/db?sslmode=verify-full&channel_binding=require"),
            (
                "postgres://u:p@h/db?channel_binding=require".to_owned(),
                Some("verify-full".to_owned())
            )
        );
        assert_eq!(
            split_sslmode("postgres://h/db"),
            ("postgres://h/db".to_owned(), None)
        );
        assert_eq!(
            split_sslmode("postgres://h/db?sslmode=disable"),
            ("postgres://h/db".to_owned(), Some("disable".to_owned()))
        );
    }

    #[test]
    fn thread_paths_are_checked() {
        assert_eq!(thread_path("/threads/t-1"), Some("t-1"));
        assert_eq!(thread_path("/threads/a b"), None);
        assert_eq!(thread_path("/threads/"), None);
        assert_eq!(thread_path("/other"), None);
    }
}
