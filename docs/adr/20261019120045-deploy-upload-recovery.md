# ADR-20261019120045: Deploy upload recovery across CLI processes

- **Status:** Accepted
- **Decision:** Find upload checkpoints by authenticated connection, source content, and deployment intent; preserve recoverable sessions when a CLI process stops.

Gregale materializes source into a temporary archive for each deploy. Recovery
keyed by that temporary pathname cannot find a previous process's upload, even
when source and options are unchanged. Cancelling server sessions after every
chunk error also discards acknowledged bytes on interruption or an outage.

Version 3 checkpoints use a SHA-256 digest of the API client's endpoint and
credential, app slug, source digest, deployment options digest, and logical
idempotency key. The pathname remains diagnostic metadata. Credentials and
logical keys are not written to checkpoints. Credential changes deliberately
isolate recovery, including different credentials for the same account. Version
2 records lack this binding and are discarded by normal local cache cleanup;
their server sessions expire under the existing TTL.

Context cancellation, request deadlines, and exhausted transient chunk retries
leave the server session and local checkpoint available. The next process asks
the server for its acknowledged offset and sends the remaining bytes. Definitive
chunk rejection retains the existing best-effort cancellation behavior. Local
recovery keeps the existing atomic mode-0600 records, per-identity process lock,
seven-day retention and 256-record bound. Source bytes are never cached locally.

An uncertain commit response keeps its checkpoint. Discovery of a committed
session retrieves its original deployment and clears the checkpoint without
starting another upload or enqueueing another build. Unknown remote status fails
without discarding recovery. An expired or cancelled session starts a replacement
with a wire retry key scoped to the previous session ID. That old checkpoint is
kept until the replacement is recorded, so a second interruption cannot replay
the original expired start response.

The existing logical deploy retry policy remains: archive deploys without secrets
derive stable intent, while secrets generate a new intent per invocation unless
the caller reuses an explicit logical key. Recovery commands therefore retain
their printed retry flag. Rollout/canary and older-server multipart fallbacks
remain single-request uploads.
