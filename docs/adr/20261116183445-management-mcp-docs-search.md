# ADR-20261116183445: The management MCP server searches the published docs

Date: 2026-11-16
Status: Accepted
Amends: ADR-20261026152009 (management MCP toolsets)

## Context

A coding agent connected to the management MCP server can inspect and change
apps, but it has no way to look up how Gregale works: which command stages a
release, what a plan's cron limit is, what an error code means. It guesses, or
the person has to paste docs pages into the conversation. The customer docs
are already published for agents: gregale.dev serves `/llms.txt` (every
published page with its title, summary and table-of-contents section),
`/llms-full.txt` (every page as Markdown, each ending with a
`Source: <page URL>` line) and `/docs/<slug>.md`. Unpublished and operator
pages are in none of them.

## Decision

Two read-only tools join the core toolset, on local and hosted connections:

- `gregale_search_docs` takes a query and returns up to ten ranked sections
  (page slug, title, heading, anchor, URL and a short snippet), at most two per
  page. Ranking is BM25 over section text with boosts for heading and page
  title matches and for sections that contain every term or the exact phrase.
  Two adjacent query words also match their joined form, so "roll back"
  finds "rollback".
- `gregale_get_docs_page` returns a page, or one section of it (an `##`
  heading with its `###` children), as Markdown with the page's heading
  anchors. Output is capped at 48 KiB and says when it was cut.

The corpus is the site's `/llms.txt` and `/llms-full.txt`, not the repository's
`docs/` directory. The site is what customers read, its anchors are the ones
results link to, and it carries only published pages. Embedding the sources
would add the 8.5 MB `docs/` tree (most of it internal) to the CLI and bridge
binaries, or need a generated copy that every docs change regenerates.

One library per process fetches the two files, about 1.4 MB, and keeps the
parsed corpus for an hour; a hosted bridge shares it across connections. A
failed refresh retries after a minute and serves the previous corpus marked
`stale` for up to 24 hours, then fails with `docs_unavailable`. Fetches go
only to the fixed site origin, refuse cross-host redirects, and are bounded in
size and time. A corpus whose pages mostly fail to match between the two
files is refused, so a format change on the site fails loudly instead of
returning empty pages.

The core catalog grows by about 2 KB; the budget in
`pkg/mcpmanagement/toolsets_test.go` records it.

## Consequences

- An agent can answer product questions from the same pages a person reads,
  and link to them.
- Results match what the site serves, which can trail `main` until the next
  docs vendor run in faas-web.
- The tools need outbound HTTPS to gregale.dev from wherever the server runs.

## Validation

`pkg/mcpmanagement/docs_search_test.go` serves fixture `llms.txt` and
`llms-full.txt` files and covers ranking, per-page limits, section reads,
heading anchors, URL page references, truncation, the hourly cache, stale
fallback and the refusal of mismatched files.
