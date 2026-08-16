# split-box deployment manifest (issue #911 / ADR-110)

This directory holds the Gregale split-box deployment manifest —
the source of truth for every host in a multi-box fleet. The
`gregalectl manifest validate` subcommand and the `make lint-manifest`
CI gate both consume this directory's contents.

## Files

- `examples/splitbox.example.yaml` — canonical split-box example
  (one control-plane host + one compute-only host). Load it with
  `gregalectl manifest validate --file=deploy/manifest/examples/splitbox.example.yaml`.

## Layout

```
deploy/manifest/
├── README.md           (this file)
└── examples/
    └── splitbox.example.yaml
```

The schema is Go-side at `pkg/manifest/`. The schema's `Validate`
function is the canonical validation path for every manifest
reader in the codebase (issue #911 explicitly requires this).
The renderer (PR-2), the release bundle installer (PR-3), and the
`gregalectl doctor` preflight (PR-4) all consume the same package.

## Quick start

```
# Validate the example manifest.
go run ./cmd/gregalectl manifest validate \
    --file=deploy/manifest/examples/splitbox.example.yaml

# Validate your own manifest.
go run ./cmd/gregalectl manifest validate --file=/path/to/splitbox.yaml
```

The validator fails closed on every missing field, every
malformed CIDR, every non-octal mode, every non-hex digest, and
every misplaced TOML table (the load-bearing check from issue
#911).

## Schema versioning

The schema is SemVer (`schema_version: 1.0.0` in the manifest
root). The validator refuses any manifest whose `schema_version`
is not in `pkg/manifest.SupportedSchemaVersions`. Bumping the
major version is a breaking change (mandatory field, renamed key,
tightened enum); minor + patch are backward-compatible.

## Required fields

See `pkg/manifest/manifest.go` for the canonical schema. The
validators are exhaustive — every required field generates a
`path: is required` error if missing.

## TOML table-placement

The renderer (PR-2) writes one TOML per daemon on each host. The
load-bearing invariant from issue #911 is that keys belong to the
right table: vmmd's `[compute_node]` block must NOT re-declare
the top-level cluster (the bug at
`deploy/ansible/roles/vmmd_service/files/vmmd.toml.example`
lines 33-40 — the canonical top-level `tls_cert_path` /
`tls_key_path` / `tls_ca_path` cluster),
and `[compute_node]` must NOT carry remote-daemon keys (e.g.
`schedd_client_*`). The
catalog at `pkg/manifest/toml_check.go` is the source of truth
for which key belongs to which table; the validator and the
renderer both consume it.
