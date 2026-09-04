# `loki` Ansible role

Installs a pinned single-binary Loki backend on a dedicated host from the
inventory's `observability` group. FaaS hosts do not run Loki; they send
platform journald records to this host through the `promtail` role.

Production requires operator-provisioned mTLS material:

- `loki_tls_ca_file` — CA used to verify Promtail clients.
- `loki_tls_cert_file` — Loki server certificate.
- `loki_tls_key_file` — private key, readable only by `root:loki`.

The role never reads secret contents. It validates the files' presence and
permissions, renders only their paths, and binds Loki to the configured
private address. Retention defaults to seven days and filesystem storage is
bounded by the host's storage policy.
