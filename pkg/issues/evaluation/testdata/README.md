# Grouping corpus

Run `make issues-grouping-eval` from the repository root. The JSON output compares
legacy, failure-site, and cause-aware grouping on the same labeled corpus.
Use `go run ./cmd/issues-grouping-eval -corpus PATH -strategy cause-chain-v1` for
an independently curated corpus.

Each case has a unique `id`, a ground-truth `family`, an event, and optional app
and environment namespaces. A false merge is a pair from different families
placed in one group. A false split is a pair from one family placed in different
groups. Precision measures same-family correctness among grouped pairs; recall
measures recovered same-family pairs. Missing denominators remain unknown.

The checked-in examples are synthetic and cover changing callers, moved lines,
stable codes, wrappers, Python stack order, Go causes without creation stacks,
manual overrides, namespaces, and HTTP fallbacks. They do not establish real-world
accuracy. Extend them with reviewed, sanitized incident labels and preserve
differences that matter operationally. Never use source contents, bodies, secrets,
or customer identifiers in a shared corpus.

The tool evaluates grouping identity, not capture delivery or symbolication
accuracy. It synthesizes valid event IDs/timestamps and removes read-only mapping
stamps while preserving reported frames. At most 10,000 cases and 8 MiB of input
are accepted. Aggregate reports omit event content and family labels.
