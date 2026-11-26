# ADR-20261118190000 Typed pipeline start previews

A pipeline MCP adapter can opt into a read-only preview with a typed summary.
Its scoped manifest advertises the summary contract. The adapter reuses the
pipeline's application authorization and passes the proposed typed input to a
read callback that captures the current source fingerprint and summary.

A portable, expiring HMAC token binds the caller's concrete receipt identity,
input digest, preview contract, and current source and summary. A fresh
confirmation reloads the source and verifies the token before reserving
the existing durable start receipt. A replay with the same authorized
receipt identity and input returns the recorded receipt ahead of preview
validation. This preserves recovery after a lost response, including
after the preview expires or the source changes. Previews use a shared
replica secret and the existing receipt and Task storage.
