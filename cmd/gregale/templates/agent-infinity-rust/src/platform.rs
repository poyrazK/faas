// The Gregale side of a slice (ADR-20261030125501 §3). The app asks for
// callback URLs with its own workload-identity token, so it holds no account
// API key. A URL posts into one thread of this app's agent inbox, and the
// consumer group turns each post into that thread's next slice.

use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use async_trait::async_trait;
use infinity_agent_core::message::InputMessage;
use infinity_agent_core::traits::InputSender;
use serde::Deserialize;
use sha2::{Digest, Sha256};

const AUDIENCE: &str = "gregale:agents";
/// Tokens live five minutes; reuse one for at most four.
const TOKEN_REUSE: Duration = Duration::from_secs(240);
/// The callback endpoint's message_id limit (AgentCallbackMessageIDMaxBytes).
const MESSAGE_ID_MAX: usize = 54;

#[derive(Debug)]
pub struct PlatformError(pub String);

impl std::fmt::Display for PlatformError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "gregale: {}", self.0)
    }
}

impl std::error::Error for PlatformError {}

impl From<reqwest::Error> for PlatformError {
    fn from(e: reqwest::Error) -> Self {
        PlatformError(e.to_string())
    }
}

pub struct Platform {
    http: reqwest::Client,
    api_url: String,
    identity_url: Option<String>,
    token: Mutex<Option<(String, Instant)>>,
}

#[derive(Deserialize)]
struct TokenResponse {
    access_token: String,
}

#[derive(Deserialize)]
struct CallbackResponse {
    url: String,
}

impl Platform {
    pub fn from_env() -> Arc<Self> {
        let api_url = std::env::var("GREGALE_API_URL")
            .unwrap_or_else(|_| "https://api.gregale.dev".to_owned());
        Arc::new(Self {
            http: reqwest::Client::builder()
                .timeout(Duration::from_secs(10))
                .build()
                .expect("bug: static client config is valid"),
            api_url: api_url.trim_end_matches('/').to_owned(),
            identity_url: std::env::var("FAAS_WORKLOAD_IDENTITY_ENDPOINT")
                .ok()
                .filter(|u| !u.is_empty()),
            token: Mutex::new(None),
        })
    }

    async fn token(&self) -> Result<String, PlatformError> {
        if let Some((token, at)) = self
            .token
            .lock()
            .expect("bug: token mutex poisoned")
            .clone()
            && at.elapsed() < TOKEN_REUSE
        {
            return Ok(token);
        }
        let base = self.identity_url.as_deref().ok_or_else(|| {
            PlatformError(
                "FAAS_WORKLOAD_IDENTITY_ENDPOINT is not set; run this on Gregale".to_owned(),
            )
        })?;
        let sep = if base.contains('?') { '&' } else { '?' };
        let res = self
            .http
            .get(format!("{base}{sep}audience={AUDIENCE}"))
            .send()
            .await?;
        if !res.status().is_success() {
            return Err(PlatformError(format!(
                "workload identity: HTTP {}",
                res.status()
            )));
        }
        let token = res.json::<TokenResponse>().await?.access_token;
        *self.token.lock().expect("bug: token mutex poisoned") =
            Some((token.clone(), Instant::now()));
        Ok(token)
    }

    async fn call(
        &self,
        path: &str,
        body: serde_json::Value,
    ) -> Result<reqwest::Response, PlatformError> {
        let res = self
            .http
            .post(format!("{}{path}", self.api_url))
            .bearer_auth(self.token().await?)
            .json(&body)
            .send()
            .await?;
        if !res.status().is_success() {
            return Err(PlatformError(format!("{path}: HTTP {}", res.status())));
        }
        Ok(res)
    }

    /// A callback URL for one thread of the inbox. Tool results and
    /// subscription events both come back through it.
    pub async fn issue_callback(
        &self,
        endpoint_id: &str,
        thread_id: &str,
    ) -> Result<String, PlatformError> {
        let res = self
            .call(
                "/v1/agent-callbacks",
                serde_json::json!({ "endpoint_id": endpoint_id, "thread_id": thread_id, "kinds": ["tool_result", "event"] }),
            )
            .await?;
        Ok(res.json::<CallbackResponse>().await?.url)
    }

    /// Stop every callback URL issued for the thread so far. A URL issued
    /// later works, so the thread can reopen.
    pub async fn revoke_callbacks(
        &self,
        endpoint_id: &str,
        thread_id: &str,
    ) -> Result<(), PlatformError> {
        self.call(
            "/v1/agent-callbacks/revocations",
            serde_json::json!({ "endpoint_id": endpoint_id, "thread_id": thread_id }),
        )
        .await?;
        Ok(())
    }

    async fn post_callback(&self, url: &str, body: serde_json::Value) -> Result<(), PlatformError> {
        let res = self.http.post(url).json(&body).send().await?;
        if !res.status().is_success() {
            return Err(PlatformError(format!(
                "callback post: HTTP {}",
                res.status()
            )));
        }
        Ok(())
    }
}

/// Infinity's loopback (InputSender): what the runtime wants to happen
/// later, such as a child thread's first message, a report to a parent or a
/// tool error, goes back into the inbox as that thread's next message. The
/// slice publishes it through a callback URL for the target thread, so it
/// needs no API key either.
#[derive(Clone)]
pub struct GregaleSender {
    pub platform: Arc<Platform>,
    pub endpoint_id: String,
}

#[async_trait]
impl InputSender for GregaleSender {
    type Error = PlatformError;

    async fn send_to_input_queue(
        &self,
        message: InputMessage,
        dedup_id: &str,
    ) -> Result<(), PlatformError> {
        let thread = message.group_id.as_str().to_owned();
        let url = self
            .platform
            .issue_callback(&self.endpoint_id, &thread)
            .await?;
        let body = serde_json::json!({
            "message_id": message_id(dedup_id),
            "kind": "event",
            "data": { "infinity_input": message },
        });
        self.platform.post_callback(&url, body).await
    }
}

/// The callback endpoint takes 1-54 bytes of letters, digits, '.', '_', ':'
/// and '-'. Other IDs map to a digest, so one dedup ID is one message ID.
pub fn message_id(id: &str) -> String {
    let valid = |c: char| c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | ':' | '-');
    if !id.is_empty() && id.len() <= MESSAGE_ID_MAX && id.chars().all(valid) {
        return id.to_owned();
    }
    let digest = Sha256::digest(id.as_bytes());
    let hex: String = digest.iter().take(16).map(|b| format!("{b:02x}")).collect();
    format!("in-{hex}")
}

#[cfg(test)]
mod tests {
    use super::message_id;

    #[test]
    fn message_ids_fit_the_callback_endpoint() {
        assert_eq!(
            message_id("rap-tool-result-call_1"),
            "rap-tool-result-call_1"
        );
        let long = message_id(&"x".repeat(80));
        assert!(long.starts_with("in-") && long.len() <= 54);
        assert_eq!(long, message_id(&"x".repeat(80)));
        assert_ne!(message_id("a b"), message_id("a c"));
    }
}
