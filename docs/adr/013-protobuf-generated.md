# ADR-013 · M1 gRPC protobuf codegen

- **Status:** accepted
- **Date:** 2026-07-16
- **Decision:** vmmd's gRPC surface ships as generated stubs from `google.golang.org/protobuf` + `google.golang.org/grpc`. The `.proto` file lives at `api/proto/onebox/faas/vmmd/v1/vmmd.proto`; generated `vmmd.pb.go` and `vmmd_grpc.pb.go` are **checked in** next to the `.proto` (using `protoc --go_opt=paths=source_relative`). Consuming code imports `github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1`.
- **Why:** (a) the spec §4.4 line 138 names "gRPC" specifically, not "JSON-RPC, REST, or message-passing". (b) `grpc-go` and `protoc-gen-go` are the platform's first non-stdlib Go deps; checking the generated code in keeps `make build` + `make test` independent of a network-on-demand protoc install — what we ship is the codegen output, not the codegen toolchain at every test run. (c) The wire contract (field numbers, package, message names) is part of the API surface; reviewers should diff that diff in a PR, not in a CI log line. (d) Spec §15 says "Conventions: ... SQL via sqlc only; no string-built queries." — analogous thinking says "no hand-rolled protobuf marshaling."
- **Consequences:**
  - Generated `*.pb.go` is committed under `pkg/vmmdgrpc/vmmdpb/`. CI's `proto-check` step runs `make proto` and `git diff --exit-code` on the generated tree to detect drift from a renamed field.
  - `Makefile` gains a `proto` target and `proto-check` target. `make build` calls `make proto` only if the `.proto` mtime is newer than the generated files.
  - PRs that touch wire shape must include both `.proto` and the diff of generated files; reviewers read the `.proto` change, not the diff of `*.pb.go` (large).
  - Future gRPC services for builderd/scheduler reuse the same Makefile `proto` target — file paths and `protoc-gen-go-grpc` opts are pinned there.
  - Adding protoc as a hard runtime dep is rejected in v1.0 — the toolchain is dev-time, codegen time only.
- **Rejected alternatives:**
  - **Hand-written protobuf via the runtime API.** Lighter footprint (no codegen step), but spec-aligned (gRPC on day one) and any daemon that needs gRPC later (builderd maybe) reuses the toolchain. Re-evaluation trigger: never — the generated version is the spec call.
  - **Plain JSON over HTTP or JSON-RPC over a unix socket.** Cheaper to ship but spec §4.4 line 138 explicitly says gRPC; deviation requires a new ADR and we don't want one per mailbox rider.
  - **Buf Schema Registry (BSR) + remote package pin.** Premature for a one-box platform; nobody outside the team is going to import our proto. Pinned to local repo for v1.

## Re-evaluation triggers

- **Gate-A multi-host (spec §16):** rename the proto package to `onebox.faas.v1.vmmd` and add `google.golang.org/grpc/credentials` to the import path for `WithInsecure` removal.
- **A second daemon grows gRPC** (most likely builderd): split a `buf.yaml` so the workspace declares vmmd + schedd + builderd as one module.
