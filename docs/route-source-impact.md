# Route impact from application source

`gregale routes impact` explains which FastAPI, Go HTTP, Express, or Hono
endpoints may be affected by a source change. It reads a local Git repository
and parses source syntax without importing or executing the application or
installing its dependencies. FastAPI analysis requires Git and `python3`; Go
analysis uses the CLI's built-in Go parser; Express and Hono analysis requires
Git and `node` and uses the bundled TypeScript 6.0.3 parser. None needs
platform credentials. To compare a source inventory with a local OpenAPI
document, see [route contract check](route-contract.md).

## Compare your current source with a baseline

```bash
gregale routes impact my-api --base origin/main --entrypoint main:app
gregale routes impact --base HEAD~1 --head HEAD --format markdown
gregale routes impact --base origin/main --path services/api \
  --entrypoint api.main:app --json --out route-impact.json
gregale routes impact --base origin/main --path services/go-api \
  --framework go-nethttp --json --out route-impact.json
gregale routes impact --base origin/main --path services/node-api \
  --framework node-http --json --out route-impact.json
```

`--framework auto` preserves FastAPI behavior when Python files are present,
selects the Go analyzer when Go files are present without Python files, and
selects the Node analyzer when JavaScript or TypeScript files are present
without Python or Go files. The `go-nethttp` analyzer covers the standard
library `ServeMux`, Chi, and Gin. The `node-http` analyzer covers Express and
Hono. Choose a framework explicitly for a mixed-language source root.
`--entrypoint` applies only to FastAPI. For Go, select the module root with
`--path`; its `go.mod` module path lets the analyzer follow local imports.
For Node, select the source root where relative imports resolve; `tsconfig`
path aliases and package-manager resolution are not evaluated.

The optional app slug is a report label. It does not bind local source to a
deployed app. `--path` selects the source directory within the repository;
module names and reported file paths are relative to that directory. For a
`src` layout, select the directory that Python uses as its import root. For Go,
select the module root so `go.mod` and its packages are in scope.

`--base` resolves to an exact commit. It does not automatically calculate a
merge base. Use the intended common ancestor when your branch and baseline
have diverged. The default candidate is the working tree, including tracked
edits, deletions, and unignored untracked route source files. Ignored untracked
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

FastAPI reports use schema version 2, Go `net/http` reports use schema version
3, and Express/Hono reports use schema version 4. `changed_files` retains raw
Git/source changes for audit, while `changed_symbols` lists added, removed, or modified
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
and deterministic SHA-256 fingerprints of captured source contents.
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
documentation endpoints, and frameworks outside the explicitly selected
FastAPI, Go HTTP, Express, and Hono analyzers are not analyzed. The model does
not validate installed packages, runtime configuration, environment
variables, data, authorization, or application
behavior. Changed non-Python files are listed and make FastAPI reports
incomplete because their effect cannot be mapped by the Python analyzer.

## Supported Go HTTP forms

The Go analyzer reads non-test `.go` files under the selected source root and
supports:

- `http.Handle` and `http.HandleFunc` registrations on the default mux.
- `Handle` and `HandleFunc` on mux variables created by `http.NewServeMux`,
  including package-level mux variables shared across source files and
  registration helpers with a `*http.ServeMux` parameter.
- Chi v4 and v5 `Get`, `Head`, `Post`, `Put`, `Patch`, `Delete`, `Connect`, `Options`,
  `Trace`, `Method`, `MethodFunc`, `Handle`, and `HandleFunc` registrations on
  `chi.NewRouter` values, `chi.Router` or `*chi.Mux` parameters, and
  package-level router variables. Literal `http.Method*` values are resolved.
- Literal Chi `Route` prefixes and `Group` callbacks, including nested groups.
  Root handlers inside `Route` groups include the exact group prefix and its
  slash form when Chi registers both.
- Chi `Mount` calls with literal prefixes and local router variables. Routes in
  the mounted router, including nested groups and mounts, are expanded under
  the mount path. A mounted router's root route includes both the exact mount
  path and its slash form when Chi registers both.
- Same-module Chi registration helpers that accept `chi.Router` or `*chi.Mux`
  parameters. Calls are expanded with the caller's route prefix and middleware,
  so one helper can describe routes registered under multiple groups.
- Locally resolvable Chi middleware passed to `Use` and `With`, scoped to the
  router, group, or route where it is registered, including aliases created
  from `With`. Middleware order is preserved, so adding, removing, or reordering
  a resolved middleware is reported as a route source change.
- Literal Go 1.22+ method/path patterns and literal path-only patterns.
- Local package handlers, `http.HandlerFunc(handler)`, anonymous handlers, and
  direct function calls through local same-module imports.
- Same-module import graphs and package-level variable or `init` dependencies.

The same Go analyzer also supports Gin routes:

- `gin.New` and `gin.Default` engines, typed `*gin.Engine` parameters, and
  statically initialized package-level engines.
- `GET`, `HEAD`, `POST`, `PUT`, `PATCH`, `DELETE`, and `OPTIONS` registrations,
  plus `Handle`, `Match` with literal methods, and `Any` (expanded to its nine
  standard HTTP methods).
- Nested literal `Group` prefixes, local group aliases, and local middleware
  added with `Use`, group handlers, or route handlers. Middleware and handler
  references contribute route-specific function evidence.
- Gin `:parameter` segments are normalized to `{parameter}` so source routes
  can join with Gregale's normalized route inventory. Gin `StaticFile` calls
  contribute their `GET` and `HEAD` routes.
- Same-module Gin registration helpers that accept typed engine or router-group
  parameters. Each call is analyzed with its own known prefix and middleware
  chain, including middleware added by the helper for later registrations.

Gin `Static` and `StaticFS` catch-all routes are listed but flagged incomplete
because they consume multiple path segments and cannot become exact route-health
selectors. Dynamic prefixes or route patterns, custom or unsupported methods,
unresolved middleware or handlers, unknown router-group prefixes, dot imports,
and route operations inside conditional or repeated control flow also make the
analysis incomplete. Registrations that rely on constructor helpers whose Gin
return type cannot be established statically remain explicit uncertainty.
Recursive helpers, helpers with dynamic router arguments, and helpers called
only from contexts the analyzer cannot reach are left unexpanded and reported
as uncertainty. Helper expansion is bounded by the same graph-depth limit as
other static route traversal.

For `ServeMux`, `GET` patterns are reported for both `GET` and the implicitly
supported `HEAD`; path-only patterns are expanded across standard HTTP methods
and marked incomplete because they also match custom methods. Chi routes use
their declared single method; `Handle` and `HandleFunc` are marked incomplete
because Chi also accepts custom methods. Chi wildcards stay in the reported path.
ServeMux method/wildcard patterns need a `go.mod` version of 1.22 or newer;
older or unknown module versions keep their semantics explicit as incomplete.
Dynamic Chi prefixes and patterns, mounts with dynamic paths or unresolved
targets, and middleware expressions that do not resolve to local functions
are also reported as incomplete. Changes to resolved middleware functions are
linked to the routes that use them. The analyzer does not type-check or build the application,
evaluate build constraints, verify that a discovered
router is attached to a running server, or infer dynamic handler expressions.
Build tags, dynamic registrations, syntax errors, unavailable local packages,
and changed non-Go files keep the report incomplete. Without `go.mod`,
same-package changes can still be linked, but cross-package resolution is
marked incomplete.

## Supported Express and Hono forms

The `node-http` analyzer reads bounded `.js`, `.jsx`, `.mjs`, `.cjs`, `.ts`,
`.tsx`, `.mts`, and `.cts` files. It supports:

- Module-level Express applications created with `express()` and routers
  created with `express.Router()` or an imported `Router` factory.
- Express HTTP verb methods, `all`, and chained `route(path).method(...)`
  registrations with literal paths.
- Module-level Hono applications created with `new Hono()`, HTTP verb methods,
  `all`, and `on()` with literal methods and paths.
- `@hono/zod-openapi` applications created with `new OpenAPIHono()` and
  `openapi()` registrations whose route method and path are literal, including
  local `createRoute()` configuration variables.
- Express `use(prefix, router)` and Hono `route(prefix, child)` mounts with
  literal prefixes and local router objects, including relative imports and
  simple re-exports.
- Local or imported named handlers and middleware, inline callbacks, and
  statically resolved function calls and callback references.
- `:parameter` segments normalized to `{parameter}`.

The analyzer does not type-check, transpile, load modules, or resolve packages.
It follows relative imports only; `tsconfig` aliases, package exports,
computed paths, app factories, route-registration helpers, conditional
registration, dynamic router mounts, unresolved function calls, nested
callbacks, unsupported constructors, and callback bindings are reported as
incomplete or remain outside the route map. Application-level middleware is
linked conservatively to all routes because path matching and registration
order are not modeled. Syntax errors, unresolved handlers or middleware,
duplicate routes, multiple application objects, and changed non-JavaScript
files keep the report incomplete. It does not verify that a discovered app is
attached to a running server.

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
Bounds in `pkg/api/limits.go` allow 20,000 paths, 1,000 Python files, 1,000
Go files, 1,000 JavaScript/TypeScript files, 1 MiB per file, 16 MiB of analyzed
source per snapshot, 1,000 routes per index, 500 registration issues per
index, 10,000 import edges, 10,000 function
symbols, 20,000 function/initializer reference edges, 2,000 symbol/initializer
issues per index, and graph depth 64. Reports allow 5,000 evidence rows and
5,000 uncertainty rows, with 4 MiB of evidence/uncertainty names and messages.
Analysis has a 60-second deadline, with 15 seconds per isolated Python or Node
parser.

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
