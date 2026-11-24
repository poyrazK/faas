# ADR-20261123180000: Agents find commands and docs by task

Date: 2026-11-23
Status: Accepted
Related: ADR-20261112223000 (agents discover and use features; `gregale guide`), ADR-20261116183445 (MCP docs search), ADR-20261023115741 (capability visibility)

## Context

The 2026-11-23 coding-agent eval (Claude and Codex, core MCP toolset, 25
runs) measured how agents look things up:

- 99 of 259 `gregale` commands (38%) were help or guide lookups, mostly
  `gregale help --all | grep …`; one agent tried `gregale search-docs`, which
  does not exist.
- The voting-app agent asked the MCP docs search about managed Redis and
  about a worker with no HTTP port. The answers were Postgres, Storage and
  companions pages: `redis` and `workers` are among the 25 docs pages held
  back from the site while their capability is in preview
  (`docs/customer-pages.json` `unpublished`). The account had managed Redis
  on; its docs were unreachable.
- `gregale guide compose-project` still said Gregale runs no Redis.

## Decision

1. **`gregale guide "<text>"`.** One word naming a guide task shows that
   page, as before. Any other text is a question: the answer lists the three
   best commands (usage line, summary, first example) and the three best docs
   sections (title › heading, link, snippet), under ~2 KB, or `--json`.
   Commands come from the manifest as the account's capability view shows
   it, one entry per command, verb and flag, plus every guide step. Guide
   steps weigh most, Common verbs and top-level commands next, flags least;
   a short synonym table maps agent words to CLI words (public →
   `--no-require-authn`, environment variable → `env`, outbound → `egress`).
2. **Docs shipped in the binary, merged with the live site.**
   `pkg/docsindex/index.json.gz` holds every published page and every page
   held back only because its capability is in preview, generated from
   `docs/` and the product catalog (`TestIndexIsCurrent`;
   `UPDATE_DOCS_INDEX=1` rewrites it). A page the live site serves wins,
   since it carries the site's summary and anything published since the
   build. The embedded index adds the pages the site lacks, and stands in for
   the whole site when the site is out of reach. The CLI keeps the site's
   agent files for a day (`gregale/docs` in the user cache directory),
   fetches them within 3 s, and never fails because the site is down.
   `GREGALE_DOCS_OFFLINE=1` skips the site.
3. **Preview pages only for accounts that have the capability.** A
   held-back preview page carries the capabilities that unlock it: those
   whose catalog `docs_url` points at it, else a capability with the page's
   name. A page with neither stays out of the index, and internal or dark
   pages are never in it. The CLI shows a preview page when its capability
   view does not hide one of them, and hides it when logged out. The MCP
   `gregale_search_docs` and `gregale_get_docs_page` apply the same rule from
   the account's capabilities.

## Consequences

- One engine (the MCP docs BM25) answers both the CLI and the MCP tools, so
  a ranking fix helps both.
- The binaries that link `pkg/mcpmanagement` grow by ~1.2 MB (the gzipped
  index; 3.6 MB of Markdown, mostly large preview pages such as mcp,
  data-api and operations).
- A docs change now needs `UPDATE_DOCS_INDEX=1 go test ./pkg/docsindex`;
  CI fails until the index is regenerated.
- Preview docs reach only the accounts that can use the feature, without
  being published.

## Follow-ups

- `docs/workers.md` does not say how a worker's health is checked; an agent
  asking about a worker "parked: liveness failed" still finds no answer.
  Document it with the worker-liveness fix.
- Nine preview pages have no catalog capability (`broker-triggers`,
  `production-protection`, `data-api-onboarding`, `events-operations`, the
  four `run-*` pages, `private-networks`) and stay out of the index until one
  points at them.
- Re-run the agent eval and compare its help-lookup share (38%) with this
  baseline.
