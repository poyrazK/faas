# ADR-20261104222700: MCP catalog SDK keys and HTTP framing

## Status

Accepted; extends ADR-20261104212930 and ADR-20261104220400.

## Context

Node's catalog accepted visible-ASCII names such as `constructor` and `__proto__`,
but the pinned SDK's ordinary object dictionaries treated them as inherited
entries. Server construction then failed on requests while process health stayed
green. Replacing `customer_record` also retained the sample URI template and
ownership/completion behavior because dispatch selected them by name.
Python required Content-Length even for valid HTTP/1 chunked requests, which can
reach the guest when the forwarding path preserves unknown request lengths.

## Decision

1. Keep the visible-ASCII name profile. A compatibility wrapper initializes the
   pinned SDK 2.4.0 tool, prompt, resource, resource-template and input-schema-cache
   dictionaries with null prototypes. Preserve the SDK objects and their public
   registration/dispatch methods. Guard the expected dictionary layout and run
   actual HTTP regressions across both supported protocols when updating the SDK.
2. Apply sample URI, ownership and completion behavior only while the original
   built-in customer handler remains registered. An explicit replacement uses
   its supplied URI/template, callback and completion behavior, subject to the
   configured resource scope policy. Application handlers enforce ownership of
   their own data, as documented for all custom resources.
3. Python decodes fixed-length and chunked bodies before JSON parsing. Preserve
   the existing 1 MiB decoded-body limit, bound aggregate chunk metadata by that
   same limit, and use the standard library's 64 KiB per-line bound for chunk
   sizes and trailers. Consume extensions and trailers without promoting them
   into authentication or dispatch headers. Reject ambiguous length/encoding
   headers, unsupported encodings, malformed chunks, truncation, forbidden
   framing trailers and oversized data. Framing errors close the connection;
   successful decoding leaves the next request boundary intact.

No platform schema, entitlement or MCP wire format changes are introduced.

## Validation

Node HTTP tests register Object.prototype property names as tools, prompts and
resource templates, list and invoke them on both protocols, replace/remove tools,
and verify inaccessible entries cannot break another catalog. Resource tests
cover a new static URI and replacement data/completions on the original template;
existing OAuth tests retain the default sample's ownership checks. Python tests
cover ordinary/chunked equivalence, exact limits, chunk extensions and trailers,
connection reuse, malformed/ambiguous framing, truncation and bounded metadata.
CLI scaffolding checks require the new decoder and regression module.
