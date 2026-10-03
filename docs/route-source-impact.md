# Route impact from application source

`gregale routes impact` explains which FastAPI HTTP endpoints may be affected
by a source change. It reads a local Git repository and parses Python syntax.
It requires Git and `python3`, uses no platform credentials, and never imports
or executes the application or installs its dependencies.

## Compare your current source with a baseline

```bash
gregale routes impact my-api --base origin/main --entrypoint main:app
gregale routes impact --base HEAD~1 --head HEAD --format markdown
gregale routes impact --base origin/main --path services/api \
  --entrypoint api.main:app --json --out route-impact.json
```

The optional app slug is a report label. It does not bind local source to a
deployed app. `--path` selects the source directory within the repository;
module names and reported file paths are relative to that directory. For a
`src` layout, select the directory that Python uses as its import root.

`--base` resolves to an exact commit. It does not automatically calculate a
merge base. Use the intended common ancestor when your branch and baseline
have diverged. The default candidate is the working tree, including tracked
edits, deletions, and unignored untracked Python files. Ignored untracked
files are excluded. `--head REF` selects committed source instead. Both
committed snapshots are read directly from Git objects without checkout,
text conversion, or external diff drivers.

Use `--entrypoint module:variable` to identify the FastAPI application.
Without it, the analyzer needs exactly one module-level FastAPI instance in
the selected source tree. Multiple instances, factories, and unresolved
entrypoints produce an incomplete report.

## What the report tells you

Each route has a method, effective path, handler file and line, registration
file and line, and one of these classifications:

| Classification | Meaning |
| --- | --- |
| `added` | A registration appears only in the candidate, and both registration indexes are complete. |
| `removed` | A registration appears only in the baseline, and both registration indexes are complete. |
| `source_changed` | The handler function AST changed, or its binding moved or changed. Comments and formatting do not change its AST. |
| `potentially_affected` | A referenced function, module initialization, registration context, or module fallback changed. |
| `no_linked_changes` | No change was linked through the supported static model. This does not prove the endpoint is unaffected. |
| `unknown` | Incomplete evidence prevents classification, including disappearance from an incomplete index. |

For a body change to `tax.rate`, an endpoint may have evidence such as:

```text
POST /checkout — potentially_affected
  Handler: checkout (checkout.py:5); registered at checkout.py:4
  Precision: function
  modified tax.py [candidate; function_reference]: checkout.checkout (checkout.py:5) -> pricing.price (pricing.py:2) -> tax.rate (tax.py:1)
```

Reports use schema version 2. `changed_files` retains raw Git/source changes
for audit, while `changed_symbols` lists added, removed, or modified
module-level functions with locations in both revisions. Function hashes
exclude AST locations, comments, and formatting. An unchanged handler and an
unreferenced function-body edit can therefore produce `no_linked_changes`
even when they share a source file.

`function_reference` evidence follows statically resolved calls and callback
references through local/imported functions, aliases, relative imports, and
simple re-exports. FastAPI dependency references in constructor, router
inclusion, route registration, defaults, and annotations contribute roots.
Module initialization calls also contribute roots: a helper invoked during
startup can affect routes without appearing in their handler call chains.
Both baseline and candidate graphs are traversed, preserving removed references.
These chains indicate potential impact; they are not runtime traces.

`module_initialization` evidence retains broader warnings for changes to
imports, globals, classes, function signatures/defaults/decorators, and
registration code. Functions invoked by module initialization can contribute
symbol chains of this kind. Router/application assembly files are direct
context; their unrelated sibling router imports are not automatically
traversed for every endpoint.

`module_fallback` evidence retains local import coverage when a reachable
call or function binding cannot be resolved. Methods, parameter calls,
conditional or repeated bindings, local callable aliases, nested functions,
comprehensions, custom decorators, mutable bindings, quoted annotations, and dynamic lookups produce explicit
uncertainties. Class middleware, models, and local references in function
type/default metadata retain their broader module coverage.

Each route reports `precision`: `function`, `mixed` (function and module
coverage), or `module_fallback`. Reachable unresolved calls make the report
incomplete and appear in per-route `uncertainties` and deduplicated report
issues. A route can still have a confirmed static addition/removal while its
function analysis is incomplete. Use `--fail-on-incomplete` to gate missing
evidence as well as `--fail-on-impact` for linked changes.

The report includes resolved commit IDs, source root, selected entrypoints,
an optional credential-free GitHub repository identity from `remote.origin.url`,
and deterministic SHA-256 fingerprints of captured Python file contents.
These identify the analyzed source; they do not establish deployment identity
or runtime coverage. Working-tree files are captured individually and can
change while analysis runs. Use committed candidates for reproducible CI.

## Supported FastAPI forms

- Module-level `FastAPI()` and `APIRouter()`, including import aliases.
- Module-level HTTP verb decorators, `api_route`, and `add_api_route`.
- Literal paths and method collections.
- Imported module-level endpoint functions.
- Local absolute and relative imports, package initialization, and simple
  re-exports.
- Nested `include_router` calls and literal router/include prefixes.
- Shared middleware and dependency references in supported registration forms.

Registrations in otherwise unimported modules are excluded. Registrations
that depend on local imports inside functions or conditional blocks are
explicit unknowns, rather than assumed present at application startup.

Factory construction, conditional or generated registration, wildcard and
dynamic imports, nonliteral paths/prefixes/methods, duplicate registrations,
router cycles, early inclusion of an unfinished router, direct object
mutation, and opaque registration helpers produce explicit issues. Parse
errors are reported without source excerpts.

Mounted applications, Starlette registration, WebSockets, framework-generated
documentation endpoints, and other frameworks are outside this first
implementation. The model does not validate installed packages, runtime
configuration, environment variables, data, authorization, or application
behavior. Changed non-Python files are listed and make the report incomplete
because their effect cannot be mapped by this Python-only analyzer.

## CI and report files

```bash
gregale routes impact --base origin/main --head HEAD --entrypoint main:app \
  --fail-on-impact --fail-on-incomplete --json
```

- Default: exit 0 after producing a report, including incomplete reports.
- `--fail-on-impact`: exit 1 for added, removed, source-changed, or potentially
  affected routes.
- `--fail-on-incomplete`: exit 1 when evidence is incomplete.
- Invalid input, unavailable tools, exceeded bounds, or failed Git operations:
  exit 1 with an error.

An unknown result alone does not trigger `--fail-on-impact`. Use both flags
when CI must stop on either known impact or missing evidence. Reports are
printed before a CI gate returns its failure.

`--format markdown` creates review-friendly output on stdout; global
`--json` selects structured output. `--out PATH` always saves the JSON report
to a new file with owner-only permissions. Existing files and symlinks are
never replaced. Reports contain source locations, symbol names, and static
reference/import chains, not function bodies, source excerpts, or configuration
values.

Analysis fails rather than silently truncating source or import evidence.
Bounds in `pkg/api/limits.go` allow 20,000 paths, 1,000 Python files, 1 MiB
per file, 16 MiB of Python source per snapshot, 1,000 routes per index,
500 registration issues per index, 10,000 import edges, 10,000 function
symbols, 20,000 function/initializer reference edges, 2,000 symbol/initializer
issues per index, and graph depth 64. Reports allow 5,000 evidence rows and
5,000 uncertainty rows, with 4 MiB of evidence/uncertainty names and messages.
Analysis has a 60-second deadline, with 15 seconds per isolated Python parser.

This complements [preview route reports](route-change-report.md) and
[route policy plans](route-policy-plans.md). Source impact identifies where
to focus review and behavioral tests; policy requirements and deployed
contract evidence answer separate questions.

## Join impact with a preview review

Use a committed `--head` matching the selected preview deployment, and a
`--base` matching its selected parent baseline:

```sh
gregale routes impact checkout --base "$BASE_COMMIT" --head "$CANDIDATE_COMMIT" \
  --path services/checkout --out impact.json
gregale preview report pr-42-checkout --source-impact impact.json \
  --test-report results.json --format markdown
```

The [preview route report](route-change-report.md#source-changes-and-review-priorities)
checks declared repository, commit, build-root, and app provenance before
combining source findings with captured contracts, deployment test samples,
and revision-attributed traffic. It exposes source reference chains and an
ordered review queue with missing-evidence actions. Metadata agreement does
not authenticate archive bytes. Working-tree reports and unresolved matches
remain separate source findings.
