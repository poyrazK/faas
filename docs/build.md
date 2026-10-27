# Builds and framework detection

`gregale deploy` inspects the source tree and selects a versioned framework
profile. Common inputs include Node (`package.json`), Python (`requirements.txt`
or `pyproject.toml`), Go (`go.mod`), and a `Dockerfile`. The inferred profile
records the package manager, start command, port, and health path in the
deployment receipt.

Go apps compile with the Go release named by the `go` line in `go.mod`;
Go functions always compile with Gregale's patched Go release. See
[Go toolchain](runtimes/go124.md#go-toolchain) for how to move an existing
Go deployment onto a release that still gets security fixes.

Inspect the decision without changing remote state:

```bash
gregale deploy --dry-run
gregale scan --explain
```

If detection cannot find a runnable profile, add an explicit Dockerfile or a
small `gregale.yaml` hosting override. Services must bind `0.0.0.0` and honor
`$PORT`; localhost-only binds and development servers are rejected before
upload where possible.

Build failures are classified as user, dependency, resource, timeout, or
platform failures. The CLI prints the failing stage and one next action. Use
`gregale release list`, `gregale release get ID`, and `gregale logs APP` to
inspect the retained evidence.

Node source builds can automatically collect source maps from the final built
image. Add `gregale.issue-source-maps.json` at the selected workload root:

```json
{"version": 1, "directory": "/app/dist"}
```

Configure the bundler to emit flat Source Map v3 files in that final directory,
then deploy normally. The build publishes sanitized maps before it can become
ready. See [automatic build source maps](issues.md#publish-source-maps-from-node-builds)
for limits, supported paths and rollout requirements.
