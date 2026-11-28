# ADR-20261128135329: Tool safety scan for sold MCP servers

- Status: Accepted (dark: API monetization routes)
- Date: 2026-11-28
- Related: ADR-20261127191645 (tool contract gate), ADR-20261126175800 (MCP
  listing), ADR-1435 (AI gateway guardrails)

## Context

A buyer who adds a sold MCP server (ADR-20261126175800) to Cursor or Claude
hands its tool definitions to their model. Those definitions are prompt
text: a tool description can tell the model to ignore its instructions,
read `~/.ssh/id_rsa`, send results to a fixed address, hide a step from the
user, or steer how other tools are called. Invisible Unicode can make such
text unreadable to a person reviewing the server. Today nothing checks what
a seller's tools say, at publish or on later deploys, and a buyer has no
signal that anyone looked.

The tool contract gate (ADR-20261127191645) already holds every sold
server's catalog at publish and lists every production candidate's catalog
before promotion, so both places can see the full text.

## Decision

Owner decisions (2026-11-28): deterministic rules, not a model; high
findings block with an audited override; tool definitions only, no runtime
scanning of tool results; a public badge on the listing and full findings
for the owner.

1. **Scanner.** `pkg/mcptoolscan.Scan` runs a versioned rule set
   (`mcptoolscan.RuleSet`, starting at 1) over a catalog contract: every
   tool, prompt, resource and resource template, their names, titles and
   descriptions, prompt arguments, and every string and property name in
   input/output schemas and annotations (not `$schema`, `$ref`, `type`,
   `format`, `pattern`). Text is lower-cased, invisible characters removed
   and whitespace collapsed before matching, so a zero-width joiner cannot
   split a phrase. A finding names the rule, severity, entry, JSON path and
   an escaped excerpt (at most `api.MCPToolScanExcerptMaxRunes` runes); a
   scan keeps at most `api.MCPToolScanFindingsMax`, highest severity first.
   The same catalog always gives the same report.
   - high: `invisible_characters` (zero-width, bidi, tag and control
     characters), `instruction_override`, `concealment` (hide from the
     user), `sensitive_file` (SSH keys, cloud credentials, MCP client
     config), `exfiltration` (send/post/forward to a literal URL or email).
   - medium: `system_prompt_reference`, `conversation_request`,
     `credential_request`, `hidden_markup` (HTML comments, hidden spans),
     `encoded_payload` (long base64/hex), `cross_tool_steering` (directives
     that name another tool), `conversation_parameter` (an input parameter
     named like `chat_history` or `system_prompt`).
   - low: `oversized_text` (over `api.MCPToolScanDescriptionMaxBytes`).
   Changing any rule bumps `RuleSet`; stored scans keep the version that
   made them.
2. **Publish.** apid scans the listing's contract on every publish (the
   contract sent, or the one a republish keeps); a listing without a
   contract has its published tool snapshot scanned. High findings refuse
   the publish with `422 mcp_tool_scan_blocked` (the findings in the
   problem's `findings` extension) unless the request carries
   `scan_review_reason` (1–512 bytes), which records the seller's review
   (`mcp_listing.tool_scan_reviewed` audit event). The report, its time
   and the review are stored on the listing.
3. **Deploy gate.** In the tool contract gate, imaged scans each production
   candidate's listed catalog and stores the report on the candidate's
   check. A high finding the effective baseline already carries as
   reviewed (same rule, entry, path and excerpt) passes; any other high
   finding fails the deployment with `mcp_tool_scan_blocked`, like a
   breaking change, unless a `tool_scan` override admits it. Overrides gain
   a kind (`contract` or `tool_scan`, default `contract`) so a reason given
   for renaming a tool never admits an injection finding, and the reverse.
   `mcp server contract allow --tool-scan` and `mcp server deploy
   --accept-scan-findings --reason TEXT` record them. A check whose high
   findings were admitted (by override or by the baseline's review) is
   marked reviewed, and that review carries forward through the baseline.
   The deployments trigger never accepts a check refused for its scan.
   A candidate whose catalog cannot be listed is handled as before
   (unverified); there is nothing to scan.
4. **Visibility.** The public listing (JSON and page) shows a badge from
   the effective baseline's scan: the scan date, rule set, and either "no
   high-severity findings" or "N findings reviewed by the seller". No
   excerpts or rules are public. The owner sees every finding on the
   listing (`GET /v1/apps/{slug}/mcp-listing`), on each check in
   `GET /v1/apps/{slug}/mcp-contract`, and in `gregale mcp server listing
   get` / `contract show`.

The scan never runs on tool results, so it adds nothing to the request
path.

## Consequences

- Buyers see that a sold server's tools were checked, and when the seller
  chose to ship something the rules flag.
- Rules are cheap and explainable but miss paraphrased attacks; a later
  ADR may add a model pass with its own costs and review flow.
- False positives cost the seller one audited reason; the review carries
  forward, so an unchanged server is not asked again.
- Release order: data (migration) → imaged + apid → CLI. Until imaged is
  released deploys are not scanned; until apid is, publishes are not.
