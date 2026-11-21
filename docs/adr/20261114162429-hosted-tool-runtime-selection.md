# ADR-20261114162429: Hosted tool publication for Node, Go and Python

Status: Accepted
Amends: ADR-20261113180001

## Decision

Allow publishing connections to retrieve all three existing CLI MCP starters and
review uploaded Go and Python application projects through the existing hosted
publication workflow. `gregale_plan_uploaded_tool_deploy` accepts `runtime` equal
to `node`, `go` or `python`. Omission means Node, preserving existing clients.

Use `pkg/markers`, shared by the CLI and native builder, to check that the declared
runtime matches the source root. Root build markers from different families are
refused, including a Dockerfile alongside a language marker. This keeps source
filtering from selecting another family after review. Node retains its existing
package start-script requirement; Go and Python use their native dependency
markers. The ordinary native build remains responsible for dependency resolution,
compilation and startup validation.

The initial review returns the normalized runtime. It is a source-derived review
fact, not a deploy flag or an independently mutable setting. The existing source
digest covers markers, commands and configuration. Apply rechecks that digest and
freezes the source before dispatch. The native journal continues retaining source
identity and catalog/fixture evidence without a new schema or runtime setting.

Keep the existing isolated builder VMs, fixed CLI command, zero-traffic candidate,
catalog pin, behavioral fixtures, guarded promotion and single-attempt native
journal. No uploaded dependency installation or customer program executes on the
MCP host. There is no new worker, VM owner, credential authority or quota.

Publication remains open-access and opt-in. Private OAuth publication and durable
Tasks retain their dedicated workflows. Publishing never creates invocation
authority; another agent needs an owner-issued grant for an exact version/tool.

## Validation

Portable hosted tests cover each runtime with inline and archive uploads on both
supported MCP protocols, repeat apply and fresh-connection receipt recovery.
Additional checks cover actual CLI starter files, incompatible/ambiguous markers,
changed source, fixture failure and unchanged consent boundaries. Existing native
build and starter protocol suites supply runtime execution coverage. Live cloud
acceptance remains a separate opt-in staging run; these tests do not establish it.
