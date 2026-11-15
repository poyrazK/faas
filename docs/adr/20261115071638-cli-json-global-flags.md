# ADR-20261115071638: Global option discovery in CLI JSON help

Date: 2026-11-15
Status: Accepted

## Context

ADR-20261114132157 exposes command-local arguments through version-1 JSON
help. Global connection and automation options remain visible only in human
root help. An agent cannot discover their value requirements or placement,
and a local `--profile` can mean resources instead of a connection. UX
specification §3.2 requires a stable, useful structured CLI surface.

## Decision

Add `global_flags` to every JSON help document without changing its schema
version or existing fields. Each entry names its accepted spellings,
description, value marker, `takes_value`, and placement. `before_command`
describes existing prefix-only options. `before_separator` describes JSON
selection in CLI arguments before `--`; forwarded application arguments keep
their existing boundaries. The existing `flags` collection remains local.

One static registry supplies global metadata and human root descriptions.
Human wording, wrapping, order and flag parsing stay unchanged. Discovery
does not read option values into metadata, authenticate, call the API, infer
defaults or alter the existing cached-capability visibility policy.

## Consequences

Agents can construct connection-scoped commands from discovery, distinguishing
global and local spellings. The additive collection preserves version-1
consumers and does not add commands, flags, environment variables or API
contracts. New global options must update the registry and dispatcher tests.

## Validation

Public-dispatcher tests cover all accepted spellings, value requirements,
placement, local profile meanings, human/JSON parity, offline discovery and
credential isolation. A built-CLI journey discovers options without requests
and constructs a profile-scoped read from another directory. Existing prefix,
JSON, forwarded-argument, command-tree and reference checks remain applicable.
