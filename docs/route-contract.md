# Route contract check

`gregale routes contract check` compares routes discovered in a local source
tree with the operations in a local OpenAPI 3.0 or 3.1 document. It uses the
static analyzers described in [route source impact](route-source-impact.md).
The command is local and offline: it needs no Gregale credentials and does not
import, build, or execute the application.

```bash
gregale routes contract check my-api \
  --openapi openapi.yaml --base origin/main --fail-on-drift --json

gregale routes contract check \
  --openapi openapi.yaml --base HEAD --path services/api \
  --framework go-nethttp --format markdown
```

The optional slug labels the report. `--base` is required; the default
candidate is the working tree, while `--head` selects a committed revision.
Use a committed `--head` in CI for reproducible results. `--path` selects the
application source root, and `--entrypoint` selects a FastAPI
`module:variable` when inference is ambiguous.

The report matches identical method and path pairs first. It then matches
different parameter names only when parameters occupy whole path segments and
the resulting method/path shape occurs exactly once in each inventory. For
example, `GET /users/{id}` can match `GET /users/{userId}` when neither side
has another route with that shape. Ambiguous shapes, incomplete source scans,
unresolved OpenAPI Path Item references, and path patterns outside that safe
normalization rule remain `unknown`.

| Finding | Meaning |
| --- | --- |
| `matched` | The source registration is represented by an OpenAPI operation. The report records whether the match was exact or used parameter-shape normalization. |
| `source_only` | A complete source inventory contains a route with no OpenAPI operation. |
| `contract_only` | A complete OpenAPI inventory contains an operation with no statically discovered source registration. |
| `unknown` | Evidence is incomplete or multiple routes could match; this is not counted as confirmed drift. |

`--fail-on-drift` exits nonzero for confirmed `source_only` or
`contract_only` routes. `--fail-on-incomplete` exits nonzero when either
inventory is inconclusive. Without those flags, the command prints the report
and exits successfully even when it finds drift, which is useful while
adopting the check. `--out` writes a JSON report to a new owner-readable file
without replacing an existing file.

The local OpenAPI file is limited to 16 MiB and 1,000 operations. The checker
compares route presence only; it does not validate request or response
schemas, security declarations, or runtime reachability. OpenAPI supports
`TRACE` operations but has no `CONNECT` operation field, so a source
`CONNECT` route cannot have a direct OpenAPI match.
