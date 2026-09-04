# `promtail` Ansible role

Installs a pinned Promtail shipper on control-plane and compute hosts. It
reads an explicit allowlist of FaaS systemd units, persists journal positions,
and sends batches asynchronously to Loki. A remote Loki outage therefore
cannot block customer requests; the bounded retry budget eventually drops
entries and exposes the loss through Promtail metrics.

Production requires a private HTTPS `promtail_loki_url` and operator-managed
mTLS files. The role validates only file metadata and renders paths, never
secret contents. Loki labels are deliberately limited to `environment`,
`host`, `unit`, `daemon`, `account_id`, and `app_id`; request IDs, instance
IDs, bodies, and arbitrary user fields remain log content rather than labels.
