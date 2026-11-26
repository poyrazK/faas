# ADR-20261118180000 Side-by-side pipeline Task contract revisions

Applications publish a stable revision family in the authorized Task
manifest. Each immutable revision has a concrete pipeline tool and native
workflow name. New bindings can resolve the family's default at generation
time. Generated revision bindings submit the expected contract SHA-256.

Receipts and Tasks stay scoped to the concrete tool. Applications publish
the previous tool as retained when promoting a new default and continue
serving its form and resource readers. A retained start checks current
authorization and loads a matching receipt without reserving or dispatching
work. This applies to both direct tool calls and MCP Task starts. Existing
handles use the same persisted binding and receipt identities after a release.
Legacy starts on unversioned adapters keep their existing request shape.
